package analyze

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"

	"github.com/dundee/gdu/v5/internal/common"
	"github.com/dundee/gdu/v5/pkg/fs"
	log "github.com/sirupsen/logrus"
)

// SequentialAnalyzer implements Analyzer
type SequentialAnalyzer struct {
	progress            *common.CurrentProgress
	progressChan        chan common.CurrentProgress
	progressOutChan     chan common.CurrentProgress
	progressDoneChan    chan struct{}
	doneChan            common.SignalGroup
	wait                *WaitGroup
	ignoreDir           common.ShouldDirBeIgnored
	followSymlinks      bool
	gitAnnexedSize      bool
	cancelled           bool
	cancelMutex         sync.Mutex
	progressDoneOnce    sync.Once
}

// CreateSeqAnalyzer returns Analyzer
func CreateSeqAnalyzer() *SequentialAnalyzer {
	return &SequentialAnalyzer{
		progress: &common.CurrentProgress{
			ItemCount: 0,
			TotalSize: int64(0),
		},
		progressChan:     make(chan common.CurrentProgress, 1),
		progressOutChan:  make(chan common.CurrentProgress, 1),
		progressDoneChan: make(chan struct{}),
		doneChan:         make(common.SignalGroup),
		wait:             (&WaitGroup{}).Init(),
	}
}

// SetFollowSymlinks sets whether symlink to files should be followed
func (a *SequentialAnalyzer) SetFollowSymlinks(v bool) {
	a.followSymlinks = v
}

// SetShowAnnexedSize sets whether to use annexed size of git-annex files
func (a *SequentialAnalyzer) SetShowAnnexedSize(v bool) {
	a.gitAnnexedSize = v
}

// GetProgressChan returns channel for getting progress
func (a *SequentialAnalyzer) GetProgressChan() chan common.CurrentProgress {
	return a.progressOutChan
}

// GetDone returns channel for checking when analysis is done
func (a *SequentialAnalyzer) GetDone() common.SignalGroup {
	return a.doneChan
}

// ResetProgress returns progress
func (a *SequentialAnalyzer) ResetProgress() {
	a.progress = &common.CurrentProgress{}
	a.progressChan = make(chan common.CurrentProgress, 1)
	a.progressOutChan = make(chan common.CurrentProgress, 1)
	a.progressDoneChan = make(chan struct{})
	a.doneChan = make(common.SignalGroup)
	a.wait = (&WaitGroup{}).Init()
	a.cancelled = false
}

// Cancel cancels the analysis gracefully
func (a *SequentialAnalyzer) Cancel() {
	a.cancelMutex.Lock()
	defer a.cancelMutex.Unlock()

	if a.cancelled {
		return
	}

	a.cancelled = true
	// Send cancellation signal to wait group and progress channels
	a.wait.Cancel()
	a.progressDoneOnce.Do(func() {
		close(a.progressDoneChan)
	})
}

// AnalyzeDir analyzes given path
func (a *SequentialAnalyzer) AnalyzeDir(
	path string, ignore common.ShouldDirBeIgnored, constGC bool,
) fs.Item {
	if !constGC {
		defer debug.SetGCPercent(debug.SetGCPercent(-1))
		go manageMemoryUsage(a.doneChan)
	}

	a.ignoreDir = ignore

	go a.updateProgress()
	dir := a.processDir(path)

	// Safely send to progressDoneChan only if not cancelled
	a.cancelMutex.Lock()
	cancelled := a.cancelled
	a.cancelMutex.Unlock()

	if !cancelled {
		a.progressDoneChan <- struct{}{}
	}
	a.doneChan.Broadcast()

	return dir
}

func (a *SequentialAnalyzer) processDir(path string) *Dir {
	var (
		file      *File
		err       error
		totalSize int64
		info      os.FileInfo
		dirCount  int
	)

	// Check if cancelled before starting
	a.cancelMutex.Lock()
	if a.cancelled {
		a.cancelMutex.Unlock()
		// Return empty directory if cancelled
		dir := &Dir{
			File: &File{
				Name: filepath.Base(path),
				Flag: '!',
			},
			ItemCount: 1,
			Files:     make(fs.Files, 0),
		}
		a.wait.Add(1)
		a.wait.Done()
		return dir
	}
	a.cancelMutex.Unlock()

	a.wait.Add(1)

	files, err := os.ReadDir(path)
	if err != nil {
		log.Print(err.Error())
	}

	dir := &Dir{
		File: &File{
			Name: filepath.Base(path),
			Flag: getDirFlag(err, len(files)),
		},
		ItemCount: 1,
		Files:     make(fs.Files, 0, len(files)),
	}
	setDirPlatformSpecificAttrs(dir, path)

	// Set BasePath early so all child paths are resolved correctly
	// Only set BasePath for absolute paths to ensure correct absolute output
	if filepath.IsAbs(path) {
		dir.BasePath = filepath.Dir(path)
	}

	for _, f := range files {
		// Check cancellation periodically
		a.cancelMutex.Lock()
		if a.cancelled {
			a.cancelMutex.Unlock()
			break
		}
		a.cancelMutex.Unlock()

		name := f.Name()
		entryPath := filepath.Join(path, name)
		if f.IsDir() {
			if a.ignoreDir(name, entryPath) {
				continue
			}
			dirCount++

			subdir := a.processDir(entryPath)
			subdir.Parent = dir
			dir.AddFile(subdir)
		} else {
			info, err = f.Info()
			if err != nil {
				log.Print(err.Error())
				dir.Flag = '!'
				continue
			}
			if a.followSymlinks && info.Mode()&os.ModeSymlink != 0 {
				infoF, err := followSymlink(entryPath, a.gitAnnexedSize)
				if err != nil {
					log.Print(err.Error())
					dir.Flag = '!'
					continue
				}
				if infoF != nil {
					info = infoF
				}
			}

			file = &File{
				Name:   name,
				Flag:   getFlag(info),
				Size:   info.Size(),
				Parent: dir,
			}
			setPlatformSpecificAttrs(file, info)

			totalSize += info.Size()

			dir.AddFile(file)
		}
	}

	// Check cancellation before sending final progress
	a.cancelMutex.Lock()
	if !a.cancelled {
		a.cancelMutex.Unlock()
		a.progressChan <- common.CurrentProgress{
			CurrentItemName: path,
			ItemCount:       len(files),
			TotalSize:       totalSize,
		}
	} else {
		a.cancelMutex.Unlock()
	}

	a.wait.Done()
	return dir
}

func (a *SequentialAnalyzer) updateProgress() {
	for {
		select {
		case <-a.progressDoneChan:
			return
		case progress := <-a.progressChan:
			a.progress.CurrentItemName = progress.CurrentItemName
			a.progress.ItemCount += progress.ItemCount
			a.progress.TotalSize += progress.TotalSize
		}

		select {
		case a.progressOutChan <- *a.progress:
		default:
		}
	}
}

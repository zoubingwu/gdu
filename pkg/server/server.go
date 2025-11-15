// Package server implements shared types for both HTTP and Unix socket servers
package server

import (
	"context"
	"sync"

	"github.com/dundee/gdu/v5/internal/common"
	"github.com/dundee/gdu/v5/pkg/analyze"
	"github.com/dundee/gdu/v5/pkg/fs"
)

// Server provides shared state and functionality for directory analysis
type Server struct {
	analyzer      common.Analyzer
	mu            sync.RWMutex
	currentDir    fs.Item
	progress      common.CurrentProgress
	isScanning    bool
	cancelFunc    context.CancelFunc
}

// NewServer creates a new server with shared analyzer
func NewServer(useStorage bool, storagePath string) *Server {
	var analyzer common.Analyzer

	if useStorage {
		// Use stored analyzer with persistent storage
		if storagePath == "" {
			storagePath = "/tmp/gdu-storage"
		}
		analyzer = analyze.CreateStoredAnalyzer(storagePath)
	} else {
		// Fall back to parallel analyzer
		analyzer = analyze.CreateAnalyzer()
	}

	return &Server{
		analyzer: analyzer,
		progress: common.CurrentProgress{},
	}
}

// DirInfo represents directory information for JSON serialization
type DirInfo struct {
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	Size         int64     `json:"size"`
	PhysicalSize int64     `json:"physical_size"`
	ItemCount    int       `json:"item_count"`
	Flag         string    `json:"flag"`
	Mtime        int64     `json:"mtime"`
	IsDir        bool      `json:"is_dir"`
	Children     []DirInfo `json:"children,omitempty"`
}

// ProgressResponse represents progress information
type ProgressResponse struct {
	IsScanning      bool   `json:"is_scanning"`
	CurrentItemName string `json:"current_item"`
	ItemCount       int    `json:"item_count"`
	TotalSize       int64  `json:"total_size"`
}

// scan performs directory scanning (shared implementation)
func (s *Server) scan(path string) {
	s.mu.Lock()
	if s.isScanning {
		s.mu.Unlock()
		return
	}
	s.isScanning = true
	s.progress = common.CurrentProgress{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.isScanning = false
		s.mu.Unlock()
	}()

	// Set up progress monitoring
	progressChan := s.analyzer.GetProgressChan()
	doneChan := s.analyzer.GetDone()

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancelFunc = cancel
	s.mu.Unlock()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case progress := <-progressChan:
				s.mu.Lock()
				s.progress = progress
				s.mu.Unlock()
			case <-doneChan:
				return
			}
		}
	}()

	// Perform the scan
	dir := s.analyzer.AnalyzeDir(path, func(name, path string) bool { return false }, false)
	dir.UpdateStats(make(fs.HardLinkedItems, 10))

	// Store the result
	s.mu.Lock()
	s.currentDir = dir
	s.mu.Unlock()

	// Cancel the progress monitor
	cancel()
}

// convertToDirInfo converts fs.Item to DirInfo for JSON serialization
func convertToDirInfo(item fs.Item, depth int) DirInfo {
	info := DirInfo{
		Name:         item.GetName(),
		Path:         item.GetPath(),
		Size:         item.GetSize(),
		PhysicalSize: item.GetUsage(),
		ItemCount:    item.GetItemCount(),
		Flag:         string(item.GetFlag()),
		Mtime:        item.GetMtime().Unix(),
		IsDir:        item.IsDir(),
		Children:     []DirInfo{},
	}

	if depth > 0 && item.IsDir() {
		if dirItem, ok := item.(interface{ GetFiles() fs.Files }); ok {
			for _, child := range dirItem.GetFiles() {
				info.Children = append(info.Children, convertToDirInfo(child, depth-1))
			}
		}
	}

	return info
}

// findDirectory finds a directory by path in the scanned tree
func findDirectory(root fs.Item, path string) fs.Item {
	if root.GetPath() == path {
		return root
	}

	if !root.IsDir() {
		return nil
	}

	if dirItem, ok := root.(interface{ GetFiles() fs.Files }); ok {
		for _, child := range dirItem.GetFiles() {
			if found := findDirectory(child, path); found != nil {
				return found
			}
		}
	}

	return nil
}

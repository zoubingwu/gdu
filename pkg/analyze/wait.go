package analyze

import "sync"

// A WaitGroup waits for a collection of goroutines to finish.
// In contrast to sync.WaitGroup Add method can be called from a goroutine.
type WaitGroup struct {
	wait   sync.Mutex
	value  int
	access sync.Mutex
	cancel chan struct{}
}

// Init prepares the WaitGroup for usage, locks
func (s *WaitGroup) Init() *WaitGroup {
	s.wait.Lock()
	if s.cancel == nil {
		s.cancel = make(chan struct{})
	}
	return s
}

// Add increments value
func (s *WaitGroup) Add(value int) {
	s.access.Lock()
	s.value += value
	s.access.Unlock()
}

// Done decrements the value by one, if value is 0, lock is released
func (s *WaitGroup) Done() {
	s.access.Lock()
	s.value--
	s.check()
	s.access.Unlock()
}

// Wait blocks until value is 0 or context is cancelled
func (s *WaitGroup) Wait() {
	s.access.Lock()
	isValue := s.value > 0
	s.access.Unlock()
	if isValue {
		// Try to wait for lock or cancellation
		go func() {
			<-s.cancel
			s.wait.Unlock()
		}()
		s.wait.Lock()
	}
}

// Cancel cancels waiting and releases all locks
func (s *WaitGroup) Cancel() {
	close(s.cancel)
}

// Reset resets the WaitGroup state
func (s *WaitGroup) Reset() {
	s.access.Lock()
	s.value = 0
	// Create new cancel channel
	if s.cancel != nil {
		close(s.cancel)
	}
	s.cancel = make(chan struct{})
	s.wait.TryLock()
	s.wait.Unlock()
	s.access.Unlock()
}

func (s *WaitGroup) check() {
	if s.value == 0 {
		s.wait.TryLock()
		s.wait.Unlock()
	}
}

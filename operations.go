package main

import (
	"context"
	"errors"
	"time"
)

func (s *DesktopService) beginOperation(name string) (context.Context, func(), error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.closing {
		return nil, nil, errors.New("程序正在退出")
	}
	if s.opCancel != nil {
		return nil, nil, errors.New("请等待当前操作结束或先取消")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.opCancel = cancel
	s.opDone = done
	finish := func() {
		cancel()
		s.opMu.Lock()
		s.opCancel = nil
		s.opDone = nil
		close(done)
		s.opMu.Unlock()
	}
	return ctx, finish, nil
}
func (s *DesktopService) CancelOperation() {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.opCancel != nil {
		s.opCancel()
	}
}

// Keep exit bounded even if a native filesystem operation has not returned.
// The application's Windows Job releases its engine descendants on process exit.
func (s *DesktopService) shutdown() {
	s.opMu.Lock()
	s.closing = true
	if s.opCancel != nil {
		s.opCancel()
	}
	done := s.opDone
	s.opMu.Unlock()
	if s.lifeCancel != nil {
		s.lifeCancel()
	}
	if s.startupCancel != nil {
		s.startupCancel()
		select {
		case <-s.startupDone:
		case <-time.After(2 * time.Second):
			return
		}
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			return
		}
	}
	if s.mu.TryLock() {
		defer s.mu.Unlock()
		s.clearConnectionImportLocked()
		s.close()
	}
}

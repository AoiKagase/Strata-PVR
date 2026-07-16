// Package operatorcontrol provides a local wake-up channel for the recording
// operator. It intentionally carries no recording data: the operator remains
// the sole owner of reservation evaluation and recording startup.
package operatorcontrol

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const notifyTimeout = 250 * time.Millisecond

// Listener receives coalesced wake-up notifications over a Unix domain
// socket. Notifications are best-effort; callers must keep their normal
// periodic polling as a fallback.
type Listener struct {
	listener net.Listener
	path     string
	wake     chan struct{}
	errs     chan error
	done     chan struct{}
	close    sync.Once
}

// Listen starts a local control socket at path. A stale socket left by a
// previous operator process is removed, but regular files are never removed.
func Listen(path string) (*Listener, error) {
	if path == "" {
		return nil, errors.New("operator control socket path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := removeStaleSocket(path); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return nil, err
	}
	l := &Listener{
		listener: ln,
		path:     path,
		wake:     make(chan struct{}, 1),
		errs:     make(chan error, 1),
		done:     make(chan struct{}),
	}
	go l.accept()
	return l, nil
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("operator control path is not a socket: %s", path)
	}
	conn, err := net.DialTimeout("unix", path, notifyTimeout)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("operator control socket is already active: %s", path)
	}
	return os.Remove(path)
}

func (l *Listener) accept() {
	for {
		conn, err := l.listener.Accept()
		if err != nil {
			select {
			case <-l.done:
				return
			default:
			}
			select {
			case l.errs <- err:
			default:
			}
			return
		}
		_ = conn.Close()
		select {
		case l.wake <- struct{}{}:
		default:
		}
	}
}

// Wake returns a channel that receives coalesced notifications.
func (l *Listener) Wake() <-chan struct{} { return l.wake }

// Errors reports an unrecoverable listener error.
func (l *Listener) Errors() <-chan error { return l.errs }

// Close stops the listener and removes its socket file.
func (l *Listener) Close() error {
	var err error
	l.close.Do(func() {
		close(l.done)
		err = l.listener.Close()
		if removeErr := os.Remove(l.path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil {
			err = removeErr
		}
	})
	return err
}

// Notify asks a running operator to re-evaluate due reservations. It does not
// wait for a recording to start and callers should treat an error as nonfatal.
func Notify(ctx context.Context, path string) error {
	if path == "" {
		return nil
	}
	dialer := net.Dialer{Timeout: notifyTimeout}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline := time.Now().Add(notifyTimeout)
	if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
		deadline = requested
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	_, err = conn.Write([]byte{1})
	return err
}

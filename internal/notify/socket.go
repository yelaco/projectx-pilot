// Package notify carries payload-free, best-effort inbox wakeups over a private
// local Unix datagram socket. SQLite, never the socket, is the durable authority.
package notify

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Listener struct {
	conn *net.UnixConn
	path string
	info os.FileInfo
	once sync.Once
}

func Listen(path string) (*Listener, error) {
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil || parent.Mode().Perm()&0077 != 0 {
		return nil, errors.New("notification directory must be owner-only")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("notification path is not a socket")
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		c.Close()
		os.Remove(path)
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		c.Close()
		return nil, err
	}
	return &Listener{conn: c, path: path, info: info}, nil
}
func (l *Listener) Close() {
	l.once.Do(func() {
		l.conn.Close()
		if info, err := os.Lstat(l.path); err == nil && os.SameFile(info, l.info) {
			_ = os.Remove(l.path)
		}
	})
}
func (l *Listener) Run(ctx context.Context, wake func()) {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			l.Close()
		case <-done:
		}
	}()
	defer close(done)
	defer l.Close()
	var b [1]byte
	for {
		_, _, err := l.conn.ReadFromUnix(b[:])
		if err != nil {
			return
		}
		wake()
	}
}
func Ping(path string) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	var d net.Dialer
	c, err := d.DialContext(ctx, "unixgram", path)
	if err != nil {
		return
	}
	defer c.Close()
	_ = c.SetWriteDeadline(time.Now().Add(25 * time.Millisecond))
	_, _ = c.Write([]byte{1})
}

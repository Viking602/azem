package desktopipc

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestIdleShutdownPreservesClientsAndActiveWork(t *testing.T) {
	now := time.Now()
	active, stopped := false, 0
	server := &Server{
		idleTimeout: time.Second, idleSince: now,
		connections: make(map[net.Conn]struct{}),
		authorizeStop: func(includeActive bool) error {
			if includeActive {
				t.Fatal("idle shutdown forced active work")
			}
			if active {
				return errors.New("busy")
			}
			return nil
		},
		onDaemonStop: func() { stopped++ },
	}
	if server.stopIfIdle(now) {
		t.Fatal("startup grace was skipped")
	}
	first, firstPeer := net.Pipe()
	defer first.Close()
	defer firstPeer.Close()
	second, secondPeer := net.Pipe()
	defer second.Close()
	defer secondPeer.Close()
	server.connections[first] = struct{}{}
	server.connections[second] = struct{}{}
	later := now.Add(2 * time.Second)
	if server.stopIfIdle(later) {
		t.Fatal("stopped connected clients")
	}
	delete(server.connections, first)
	if server.stopIfIdle(later) {
		t.Fatal("stopped remaining client")
	}
	delete(server.connections, second)
	active = true
	if server.stopIfIdle(later) {
		t.Fatal("stopped detached active work")
	}
	active = false
	// A reconnect during the grace period must also keep the daemon alive.
	server.connections[first] = struct{}{}
	if server.stopIfIdle(later) {
		t.Fatal("stopped reconnected client")
	}
	delete(server.connections, first)
	if !server.stopIfIdle(later) || stopped != 1 {
		t.Fatal("did not stop after work completed")
	}
	if server.stopIfIdle(later) || stopped != 1 {
		t.Fatal("stopped twice")
	}
}

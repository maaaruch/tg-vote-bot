package session

import "sync"

type Session struct {
	ActiveRoomID                   int64
	WaitingMediaForNomineeID       int64
	CreatingNomineeForNominationID int64
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[int64]*Session
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[int64]*Session),
	}
}

func (m *Manager) Get(userID int64) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := m.sessions[userID]
	if s == nil {
		s = &Session{}
		m.sessions[userID] = s
	}
	return s
}

package smpp

import (
	"context"
	"errors"
	"github.com/rs/xid"
	"github.com/sujit-baniya/protocol/smpp/balancer"
	"github.com/sujit-baniya/protocol/smpp/pdu"
	"golang.org/x/time/rate"
	"sync"
	"time"
)

type ManagerInterface interface {
	Start() error
	AddSession(noOfSession ...int) (string, error)
	GetSession(sessionId ...string) *Session
	RemoveSession(sessionId ...string) error
	Rebind(sessionId ...string)
	Submit(pd pdu.PDU, sessionId ...string) error
	Close(noOfSession ...int) error
}

type ManagerConfig struct {
	Name           string
	Slug           string
	Auth           Auth
	Settings       Settings
	RebindDuration time.Duration
	MaxSession     int
	Balancer       balancer.Balancer
}

type Manager struct {
	Name        string
	Slug        string
	ID          string
	Session     map[string]*Session
	balancer    balancer.Balancer
	SessionIDs  []string
	Config      ManagerConfig
	MaxSession  int
	RebindWait  *rate.Limiter
	RateLimiter *rate.Limiter
	rwctx       context.Context
	lmctx       context.Context
	mu          sync.RWMutex
}

func NewSessionManager(cfg ManagerConfig) ManagerInterface {
	if cfg.MaxSession == 0 {
		cfg.MaxSession = 4
	}
	if cfg.Balancer == nil {
		cfg.Balancer = &balancer.RoundRobin{}
	}
	return &Manager{
		Name:       cfg.Name,
		ID:         xid.New().String(),
		Config:     cfg,
		balancer:   cfg.Balancer,
		MaxSession: cfg.MaxSession,
	}
}

func (m *Manager) Start() error {
	if m.Session == nil {
		_, err := m.AddSession(m.MaxSession)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) AddSession(noOfSession ...int) (string, error) {
	if len(noOfSession) == 0 {
		return m.addSession()
	}
	if noOfSession[0] > m.MaxSession {
		return "", errors.New("Can't create more than allowed no of sessions.")
	}
	if (len(m.Session) + noOfSession[0]) > m.MaxSession {
		return "", errors.New("There are active sessions. Can't create more than allowed no of sessions.")
	}
	for i := 0; i < noOfSession[0]; i++ {
		_, err := m.addSession()
		if err != nil {
			return "", err
		}
	}
	return "", nil
}

func (m *Manager) RemoveSession(sessionId ...string) error {
	if len(sessionId) > 0 {
		return m.close(sessionId[0])
	} else {
		return m.Close()
	}
}

func (m *Manager) Close(noOfSession ...int) error {
	if len(noOfSession) == 0 {
		for id := range m.Session {
			err := m.close(id)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) Rebind(sessionId ...string) {
	if len(sessionId) == 0 {
		if session, ok := m.Session[sessionId[0]]; ok {
			session.rebind()
		}
	}
}

func (m *Manager) Submit(pd pdu.PDU, sessionId ...string) error {
	session := m.getSession(sessionId...)
	if session == nil {
		return errors.New("Session not found")
	}
	err := session.Wait()
	if err != nil {
		return err
	}
	return session.Transceiver().Submit(pd)
}

func (m *Manager) GetSession(sessionIDs ...string) *Session {
	return m.getSession(sessionIDs...)
}

func (m *Manager) getSession(sessionIDs ...string) *Session {
	var pickedID string
	if len(sessionIDs) > 0 { // pick among custom
		pickedID, _ = m.balancer.Pick(sessionIDs)
		if session, ok := m.Session[pickedID]; ok {
			return session
		}
	}

	// pick among managing session
	pickedID, _ = m.balancer.Pick(m.SessionIDs)
	session, _ := m.Session[pickedID]
	return session
}

func (m *Manager) addSession() (string, error) {
	if m.Session == nil {
		m.Session = make(map[string]*Session)
	}
	session, err := NewSession(
		TRXConnector(NonTLSDialer, m.Config.Auth),
		m.Config.Settings,
		m.Config.RebindDuration,
	)
	if err != nil {
		return "", err
	}
	m.Session[session.ID] = session
	m.SessionIDs = append(m.SessionIDs, session.ID)
	return session.ID, nil
}

func (m *Manager) close(sessionId string) error {
	if !m.Session[sessionId].IsClosed() {
		err := m.Session[sessionId].close()
		if err != nil {
			return err
		}
	}
	return nil
}

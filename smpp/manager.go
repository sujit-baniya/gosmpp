package smpp

import (
	"context"
	"fmt"
	"github.com/go-errors/errors"
	"github.com/sujit-baniya/protocol/smpp/balancer"
	"github.com/sujit-baniya/protocol/smpp/coding"
	"github.com/sujit-baniya/protocol/smpp/pdu"
	"log"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rs/xid"
)

type ConnectionInterface interface {
	Send(packet interface{}) (err error)
	Throttle() error
}

type ManagerInterface interface {
	Start() error
	AddConnection(noOfConnection ...int) error
	RemoveConnection(connectionID ...string) error
	GetConnection(conIds ...string) (*Session, error)
	SetupConnection() error
	Rebind() error
	Send(payload interface{}, connectionID ...string) (interface{}, error)
	Close(connectionID ...string) error
}

type Setting struct {
	Name             string
	Slug             string
	URL              string
	Auth             Auth
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	EnquiryInterval  time.Duration
	EnquiryTimeout   time.Duration
	MaxConnection    int
	Balancer         balancer.Balancer
	Throttle         int
	UseAllConnection bool
	HandlePDU        func(con *Session)
	OnPDU            PDUCallback
	AutoRebind       bool
}

type Manager struct {
	Name        string
	Slug        string
	ID          string
	ctx         context.Context
	setting     Setting
	connections map[string]*Session
	Balancer    balancer.Balancer
	connIDs     []string
	mu          sync.RWMutex
}

type HandlePDU func(conn *Session)

type Message struct {
	From    string
	To      string
	Message string
}

func NewManager(setting Setting) (*Manager, error) {
	if setting.MaxConnection == 0 {
		setting.MaxConnection = 1
	}
	manager := &Manager{
		Name:        setting.Name,
		Slug:        setting.Slug,
		ID:          xid.New().String(),
		ctx:         context.Background(),
		setting:     setting,
		connections: make(map[string]*Session),
	}
	if setting.Balancer == nil {
		manager.Balancer = &balancer.RoundRobin{}
	}
	return manager, nil
}

func (m *Manager) Start() error {
	if m.setting.UseAllConnection {
		for i := 0; i < m.setting.MaxConnection; i++ {
			err := m.SetupConnection()
			if err != nil {
				return err
			}
		}
		return nil
	}
	if len(m.connIDs) == 0 {
		err := m.SetupConnection()
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) AddConnection(noOfConnection ...int) error {
	con := 1
	if len(noOfConnection) > 0 {
		con = noOfConnection[0]
	}
	if con > m.setting.MaxConnection {
		return errors.New("Can't create more than allowed no of connections.")
	}
	if (len(m.connIDs) + con) > m.setting.MaxConnection {
		return errors.New("There are active sessions. Can't create more than allowed no of sessions.")
	}
	connLeft := m.setting.MaxConnection - len(m.connIDs)
	n := 0
	if connLeft >= con {
		n = con
	} else {
		n = connLeft
	}
	for i := 0; i < n; i++ {
		err := m.SetupConnection()
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) RemoveConnection(conID ...string) error {
	if len(conID) > 0 {
		for _, id := range conID {
			if con, ok := m.connections[id]; ok {
				err := con.Close()
				if err != nil {
					return err
				}
				m.connIDs = remove(m.connIDs, id)
				delete(m.connections, id)
			}
		}
	} else {
		for id, con := range m.connections {
			err := con.Close()
			if err != nil {
				return err
			}
			m.connIDs = remove(m.connIDs, id)
			delete(m.connections, id)
		}
	}
	return nil
}

func (m *Manager) Rebind() error {
	err := m.Close()
	if err != nil {
		return err
	}
	m.connections = make(map[string]*Session)
	m.connIDs = []string{}
	err = m.Start()
	if err != nil {
		return err
	}
	return m.HandlePDU()
}

func (m *Manager) SetupConnection() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := net.Dial("tcp", m.setting.URL)
	if err != nil {
		return err
	}

	smppSetting := Settings{
		EnquireLink:  m.setting.EnquiryInterval,
		WriteTimeout: m.setting.WriteTimeout,
		ReadTimeout:  m.setting.ReadTimeout,

		OnSubmitError: func(_ pdu.PDU, err error) {
			log.Fatal("SubmitPDU error:", err)
		},

		OnReceivingError: func(err error) {
			fmt.Println("Receiving PDU/Network error:", err)
		},

		OnRebindingError: func(err error) {
			fmt.Println("Rebinding but error:", err)
		},

		OnPDU: m.setting.OnPDU,

		OnClosed: func(state State) {
			fmt.Println(state)
		},
	}
	conn, err := NewSession(TRXConnector(NonTLSDialer, m.setting.Auth), smppSetting, m.setting.EnquiryTimeout)
	if err != nil {
		return err
	}
	m.connIDs = append(m.connIDs, conn.ID)
	m.connections[conn.ID] = conn
	return nil
}

func (m *Manager) GetConnection(conIds ...string) (*Session, error) {
	var pickedID string
	if len(conIds) > 0 { // pick among custom
		pickedID, err := m.Balancer.Pick(conIds)
		if err != nil {
			return nil, err
		}
		if con, ok := m.connections[pickedID]; ok {
			return con, nil
		}
	}

	// pick among managing session
	pickedID, err := m.Balancer.Pick(m.connIDs)
	if err != nil {
		return nil, err
	}
	if con, ok := m.connections[pickedID]; ok {
		return con, nil
	}
	return nil, errors.New("no connection")
}

type SmppResponse struct {
	SubmitSM     *pdu.SubmitSM     `json:"submit_sm"`
	SubmitSMResp *pdu.SubmitSMResp `json:"submit_sm_resp"`
}

func (m *Manager) Send(payload interface{}, connectionId ...string) (interface{}, error) {
	sms := payload.(Message)
	shortMessages, err := m.Compose(sms.Message)
	if err != nil {
		panic(err)
	}

	var responses []SmppResponse
	responseChan := make(chan map[*pdu.SubmitSM]*pdu.SubmitSMResp)
	wg := &sync.WaitGroup{}
	for _, shortMessage := range shortMessages {
		wg.Add(1)
		go m.SendShortMessage(sms.From, sms.To, shortMessage, wg, responseChan, connectionId...)
	}
	go func() {
		wg.Wait()
		close(responseChan)
	}()
	for response := range responseChan {
		for submitSM, submitSMResp := range response {
			responses = append(responses, SmppResponse{
				SubmitSM:     submitSM,
				SubmitSMResp: submitSMResp,
			})
		}
	}
	return responses, nil
}

func (m *Manager) SendShortMessage(from string, to string, shortMessage pdu.ShortMessage, wg *sync.WaitGroup, responseChan chan<- map[*pdu.SubmitSM]*pdu.SubmitSMResp, connectionId ...string) error {
	defer wg.Done()
	conn, err := m.GetConnection(connectionId...)
	if err != nil {
		return err
	}
	packet := m.Prepare(from, to, shortMessage)
	err = conn.Wait()
	if err != nil {
		return err
	}
	err = conn.Transceiver().Submit(packet)
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) Prepare(from string, to string, shortMessage pdu.ShortMessage) *pdu.SubmitSM {
	return &pdu.SubmitSM{
		SourceAddr:         parseSrcPhone(from),
		DestAddr:           parseDestPhone(to),
		RegisteredDelivery: 1,
		Message:            shortMessage,
		EsmClass:           0,
	}
}

func (m *Manager) Close(connectionId ...string) error {
	if len(connectionId) > 0 {
		if con, ok := m.connections[connectionId[0]]; ok {
			err := con.Close()
			if err != nil {
				return err
			}
		}
	} else {
		for _, conn := range m.connections {
			err := conn.Close()
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (m *Manager) HandlePDU() error {
	for _, conn := range m.connections {
		go m.setting.HandlePDU(conn)
	}
	return nil
}

func (m *Manager) Compose(msg string) ([]pdu.ShortMessage, error) {
	return Compose(msg)
}

func Compose(msg string) ([]pdu.ShortMessage, error) {
	reference := uint16(rand.Intn(0xFFFF))
	dataCoding := coding.BestSafeCoding(msg)
	return pdu.ComposeMultipartShortMessage(msg, dataCoding, reference)
}

func parseSrcPhone(phone string) pdu.Address {
	srcAddress := pdu.NewAddress()
	if strings.HasPrefix(phone, "+") {
		srcAddress.SetTon(1)
		srcAddress.SetNpi(1)
		_ = srcAddress.SetAddress(phone)
		return srcAddress
	}

	if utf8.RuneCountInString(phone) <= 5 {
		srcAddress.SetTon(3)
		srcAddress.SetNpi(0)
		_ = srcAddress.SetAddress(phone)
		return srcAddress
	}
	if isLetter(phone) {

		srcAddress.SetTon(5)
		srcAddress.SetNpi(0)
		_ = srcAddress.SetAddress(phone)
		return srcAddress
	}

	srcAddress.SetTon(1)
	srcAddress.SetNpi(1)
	_ = srcAddress.SetAddress(phone)
	return srcAddress
}

func parseDestPhone(phone string) pdu.Address {
	destAddress := pdu.NewAddress()
	if strings.HasPrefix(phone, "+") {
		destAddress.SetTon(1)
		destAddress.SetNpi(1)
		_ = destAddress.SetAddress(phone)
		return destAddress
	}
	destAddress.SetTon(0)
	destAddress.SetNpi(1)
	_ = destAddress.SetAddress(phone)
	return destAddress
}

func isLetter(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func remove(s []string, r string) []string {
	for i, v := range s {
		if v == r {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

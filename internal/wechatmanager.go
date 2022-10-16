package internal

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/process"

	log "github.com/sirupsen/logrus"
)

var (
	once     sync.Once
	instance *WechatManager
)

const (
	listenPort        = 22222
	ClientInitTiemout = 10 * time.Second
)

type WechatManager struct {
	as *AppService

	funcNewWechat   uintptr
	funcStartListen uintptr
	funcStopListen  uintptr

	pids        map[int]string
	clients     map[string]*WechatClient
	clientsLock sync.RWMutex
	portSeq     int32
}

func GetWechatManager() *WechatManager {
	once.Do(func() {
		instance = &WechatManager{}

		instance.pids = make(map[int]string)
		instance.clients = make(map[string]*WechatClient)
		instance.portSeq = listenPort

		var driverDLL string
		if runtime.GOARCH == "amd64" {
			driverDLL = "wxDriver64.dll"
		} else {
			driverDLL = "wxDriver.dll"
		}
		driver, err := syscall.LoadLibrary(driverDLL)
		if err != nil {
			log.Fatal(err)
		}
		defer syscall.FreeLibrary(driver)

		newWechat, err := syscall.GetProcAddress(driver, "new_wechat")
		if err != nil {
			log.Fatal(err)
		}
		startListen, err := syscall.GetProcAddress(driver, "start_listen")
		if err != nil {
			log.Fatal(err)
		}
		stopListen, err := syscall.GetProcAddress(driver, "stop_listen")
		if err != nil {
			log.Fatal(err)
		}
		instance.funcNewWechat = newWechat
		instance.funcStartListen = startListen
		instance.funcStopListen = stopListen

	})
	return instance
}

func (m *WechatManager) Dispose() {
	m.clientsLock.RLock()
	defer m.clientsLock.RUnlock()

	for _, client := range m.clients {
		client.Dispose()
	}
}

func (m *WechatManager) Connect(mxid string, path string) error {
	m.clientsLock.Lock()
	defer m.clientsLock.Unlock()

	client, ok := m.clients[mxid]
	if ok && client.IsAlive() {
		return nil
	}

	client = &WechatClient{
		port: atomic.AddInt32(&m.portSeq, 1),
	}
	pid, _, errno := syscall.SyscallN(m.funcNewWechat)
	if pid == 0 {
		return errno
	}
	if int(errno) != 0 {
		log.Infoln(errno)
	}
	client.pid = pid

	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return fmt.Errorf("wechat process not exists: %w", err)
	}
	client.proc = p

	_, _, errno = syscall.SyscallN(m.funcStartListen, pid, uintptr(client.port))
	if int(errno) != 0 {
		client.Dispose()
		return errno
	}

	m.pids[int(pid)] = mxid
	m.clients[mxid] = client

	ctx, cancel := context.WithTimeout(context.Background(), ClientInitTiemout)
	defer cancel()

	for {
		err = client.HookMsg(path)
		if err == nil {
			return nil
		}

		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return err
		}
	}
}

func (m *WechatManager) Disconnet(mxid string) (err error) {
	m.clientsLock.Lock()
	defer m.clientsLock.Unlock()

	if client, ok := m.clients[mxid]; ok {
		err = client.Dispose()
		delete(m.pids, int(client.pid))
		delete(m.clients, mxid)
	}
	return
}

func (m *WechatManager) LoginWtihQRCode(mxid string) ([]byte, error) {
	m.clientsLock.RLock()
	defer m.clientsLock.RUnlock()

	client, ok := m.clients[mxid]
	if !ok {
		return nil, fmt.Errorf("client not found")
	}

	return client.LoginWtihQRCode()
}

func (m *WechatManager) IsLogin(mxid string) (*IsLoginData, error) {
	m.clientsLock.RLock()
	defer m.clientsLock.RUnlock()

	client, ok := m.clients[mxid]
	if !ok {
		return nil, fmt.Errorf("client not found")
	}

	return &IsLoginData{Status: client.IsLogin()}, nil
}

func (m *WechatManager) GetSelf(mxid string) (*UserInfo, error) {
	m.clientsLock.RLock()
	defer m.clientsLock.RUnlock()

	client, ok := m.clients[mxid]
	if !ok {
		return nil, fmt.Errorf("client not found")
	}

	return client.GetSelf()
}

func (m *WechatManager) GetUserInfo(mxid, wxid string) (*UserInfo, error) {
	m.clientsLock.RLock()
	defer m.clientsLock.RUnlock()

	client, ok := m.clients[mxid]
	if !ok {
		return nil, fmt.Errorf("client not found")
	}

	return client.GetUserInfo(wxid)
}

func (m *WechatManager) GetGroupInfo(mxid, wxid string) (*GroupInfo, error) {
	m.clientsLock.RLock()
	defer m.clientsLock.RUnlock()

	client, ok := m.clients[mxid]
	if !ok {
		return nil, fmt.Errorf("client not found")
	}

	return client.GetGroupInfo(wxid)
}

func (m *WechatManager) GetGroupMembers(mxid, wxid string) ([]string, error) {
	m.clientsLock.RLock()
	defer m.clientsLock.RUnlock()

	client, ok := m.clients[mxid]
	if !ok {
		return nil, fmt.Errorf("client not found")
	}

	return client.GetGroupMembers(wxid)
}

func (m *WechatManager) GetGroupMemberNickname(mxid, group, wxid string) (string, error) {
	m.clientsLock.RLock()
	defer m.clientsLock.RUnlock()

	client, ok := m.clients[mxid]
	if !ok {
		return "", fmt.Errorf("client not found")
	}

	return client.GetGroupMemberNickname(group, wxid)
}

func (m *WechatManager) GetFriendList(mxid string) ([]*UserInfo, error) {
	m.clientsLock.RLock()
	defer m.clientsLock.RUnlock()

	client, ok := m.clients[mxid]
	if !ok {
		return nil, fmt.Errorf("client not found")
	}

	return client.GetFriendList()
}

func (m *WechatManager) GetGroupList(mxid string) ([]*GroupInfo, error) {
	m.clientsLock.RLock()
	defer m.clientsLock.RUnlock()

	client, ok := m.clients[mxid]
	if !ok {
		return nil, fmt.Errorf("client not found")
	}

	return client.GetGroupList()
}

func (m *WechatManager) SendMessage(mxid string, msg *MatrixMessage) error {
	m.clientsLock.RLock()
	defer m.clientsLock.RUnlock()

	client, ok := m.clients[mxid]
	if !ok {
		return fmt.Errorf("client not found")
	}

	switch msg.MessageType {
	case "m.text":
		var mentions []string
		if err := json.Unmarshal(msg.Data, &mentions); err != nil {
			return client.SendText(msg.Target, msg.Content)
		} else {
			return client.SendAtText(msg.Target, msg.Content, mentions)
		}
	case "m.image", "m.video":
		path := saveBlob(m.as, msg)
		if len(path) > 0 {
			return client.SendImage(msg.Target, path)
		}
	case "m.file":
		path := saveBlob(m.as, msg)
		if len(path) > 0 {
			return client.SendFile(msg.Target, path)
		}
	}

	return nil
}

func (m *WechatManager) ForwardMessage(mxid string, target string, msgid uint64) error {
	m.clientsLock.RLock()
	defer m.clientsLock.RUnlock()

	client, ok := m.clients[mxid]
	if !ok {
		return fmt.Errorf("client not found")
	}

	return client.ForwardMessage(target, msgid)
}

func (m *WechatManager) Serve(as *AppService) {
	m.as = as

	addr := fmt.Sprintf("127.0.0.1:%d", listenPort)
	log.Infoln("WechatManager starting to listen on", addr)
	listen, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}

	for {
		conn, err := listen.Accept()
		if err != nil {
			log.Fatal(err)
		}

		log.Debugln("new connecting!")

		go func(conn net.Conn) {
			defer conn.Close()

			for {
				data, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					if err != io.EOF {
						log.Warnln(err)
					}
					return
				}
				msg := WechatMessage{
					IsSendByPhone: 1,
				}
				if err := json.Unmarshal(data, &msg); err != nil {
					log.Warnln(err)
					conn.Write([]byte("500 ERROR"))
				} else {
					if mxid, ok := m.pids[msg.PID]; ok {
						go as.handleWechatMessage(mxid, &msg)
					} else {
						log.Warnln("Failed to map pid to remote mxid.")
					}
					conn.Write([]byte("200 OK"))
				}
			}
		}(conn)
	}
}

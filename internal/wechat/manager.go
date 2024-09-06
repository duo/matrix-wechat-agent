package wechat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
	"os/exec"

	"github.com/duo/matrix-wechat-agent/internal/common"

	"github.com/shirou/gopsutil/v3/process"

	"go.zoe.im/injgo"

	log "github.com/sirupsen/logrus"
)

type Manager struct {
	config *common.Configure

	portSeq int32

	pids        map[int]string
	clients     map[string]*Client
	clientsLock sync.Mutex

	mutex       common.KeyMutex
	processFunc func(string, *WechatMessage)
}

func NewManager(config *common.Configure, f func(string, *WechatMessage)) *Manager {
	return &Manager{
		config:          config,
		portSeq:         config.Wechat.ListenPort,
		pids:            make(map[int]string),
		clients:         make(map[string]*Client),
		mutex:           common.NewHashed(47),
		processFunc:     f,
	}
}

func (m *Manager) Connect(mxid string, path string) error {
	log.Debugln("Create WeChat for mxid: ", mxid)
	m.clientsLock.Lock()
	defer m.clientsLock.Unlock()

	client, ok := m.clients[mxid]
	if ok && client.IsAlive() {
		return nil
	}

	// TODO: write port to config file similar to this
	// https://github.com/cocktail18/wxhelper-go/blob/2d6c13f455f8333d35d1410e261284f944483ea3/injector/injector.go#L20
	// client = &Client{
	// 	listen: m.config.Wechat.ListenPort,
	// 	port:   atomic.AddInt32(&m.portSeq, 1),
	// }
	client = &Client{
		listen: 19088,
		port:   19088,
	}


	// Start WeChat process
	log.Debugln("Starting WeChat process at ", m.config.Wechat.Path)
	wechat := exec.Command(m.config.Wechat.Path)
	errWechat := wechat.Start()
	if errWechat != nil {
		return fmt.Errorf("Failed to start wechat: %w", m.config.Wechat.Path, errWechat)
	}
	client.pid = wechat.Process.Pid
	p, err := process.NewProcess(int32(client.pid))
	if err != nil {
		return fmt.Errorf("Wechat process does not exist: %w", err)
	}
	client.proc = p

	// Wait for WeChat process to start
	<-time.After(1 * time.Second)

	// Attach to WeChat process
	log.Debugln("Injecting WeChat helper from ", m.config.Wechat.WxHelperPath)
	errWxHelper := injgo.Inject(client.pid, m.config.Wechat.WxHelperPath, true)
	if errWxHelper != nil {
		client.Dispose()
		return fmt.Errorf("failed to inject wxhelper: %w", m.config.Wechat.WxHelperPath, errWxHelper)
	}

	m.pids[client.pid] = mxid
	m.clients[mxid] = client

	ctx, cancel := context.WithTimeout(context.Background(), m.config.Wechat.InitTimeout)
	defer cancel()

	for {
		log.Debugln("Initiate WeChat helper")
		err = client.HookMsg(path)
		if err == nil {
			log.Infoln("Hooked wechat message")
			if err := client.SetVersion(m.config.Wechat.Version); err != nil {
				log.Warnln("Failed to set version", err)
			} else {
				log.Infoln("Set wechat version to", m.config.Wechat.Version)
			}
			return nil
		} else {
			log.Debugln("Failed to connect to Wechat helper", err)
		}

		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return err
		}
	}
}

func (m *Manager) Disconnect(mxid string) (err error) {
	m.clientsLock.Lock()
	defer m.clientsLock.Unlock()

	if client, ok := m.clients[mxid]; ok {
		err = client.Dispose()
		delete(m.pids, int(client.pid))
		delete(m.clients, mxid)
	}
	return
}

func (m *Manager) LoginWithQRCode(mxid string) (any, error) {
	return m.call(mxid, func(c *Client, v ...any) (any, error) {
		return c.LoginWithQRCode()
	})
}

func (m *Manager) IsLogin(mxid string) (any, error) {
	return m.call(mxid, func(c *Client, v ...any) (any, error) {
		return c.IsLogin(), nil
	})
}

func (m *Manager) GetSelf(mxid string) (any, error) {
	return m.call(mxid, func(c *Client, v ...any) (any, error) {
		info, err := c.GetSelf()
		return info.toUserInfo(), err
	})
}

func (m *Manager) GetUserInfo(mxid string, wxid string) (any, error) {
	return m.call(mxid, func(c *Client, v ...any) (any, error) {
		info, err := c.GetUserInfo(v[0].(string))
		return info.toUserInfo(), err
	}, wxid)
}

func (m *Manager) GetGroupInfo(mxid string, wxid string) (any, error) {
	return m.call(mxid, func(c *Client, v ...any) (any, error) {
		info, err := c.GetGroupInfo(v[0].(string))
		return info.toGroupInfo(), err
	}, wxid)
}

func (m *Manager) GetGroupMembers(mxid string, wxid string) (any, error) {
	return m.call(mxid, func(c *Client, v ...any) (any, error) {
		return c.GetGroupMembers(v[0].(string))
	}, wxid)
}

func (m *Manager) GetGroupMemberNickname(mxid, group, wxid string) (any, error) {
	return m.call(mxid, func(c *Client, v ...any) (any, error) {
		return c.GetGroupMemberNickname(v[0].(string), v[1].(string))
	}, group, wxid)
}

func (m *Manager) GetFriendList(mxid string) (any, error) {
	return m.call(mxid, func(c *Client, v ...any) (any, error) {
		friends := []*common.UserInfo{}
		info, err := c.GetFriendList()
		for _, i := range info {
			friends = append(friends, i.toUserInfo())
		}
		return friends, err
	})
}

func (m *Manager) GetGroupList(mxid string) (any, error) {
	return m.call(mxid, func(c *Client, v ...any) (any, error) {
		groups := []*common.GroupInfo{}
		info, err := c.GetGroupList()
		for _, i := range info {
			groups = append(groups, i.toGroupInfo())
		}
		return groups, err
	})
}

func (m *Manager) SendMessage(mxid string, event *common.Event) (*common.Event, error) {
	m.clientsLock.Lock()
	client, ok := m.clients[mxid]
	m.clientsLock.Unlock()

	if !ok {
		return nil, fmt.Errorf("client not found")
	}

	var err error
	target := event.Chat.ID
	switch event.Type {
	case common.EventText:
		if len(event.Mentions) > 0 {
			err = client.SendAtText(target, event.Content, event.Mentions)
		} else {
			err = client.SendText(target, event.Content)
		}
	case common.EventPhoto, common.EventSticker, common.EventVideo:
		path := saveBlob(m.config.Wechat.Workdir, event)
		if len(path) > 0 {
			err = client.SendImage(target, path)
		} else {
			err = fmt.Errorf("failed to download media")
		}
	case common.EventFile:
		path := saveBlob(m.config.Wechat.Workdir, event)
		if len(path) > 0 {
			err = client.SendFile(target, path)
		} else {
			err = fmt.Errorf("failed to download file")
		}
	default:
		err = fmt.Errorf("event type not support: %s", event.Type)
	}

	return &common.Event{
		ID:        fmt.Sprint(time.Now().UnixMilli()),
		Timestamp: time.Now().UnixMilli(),
	}, err
}

func (m *Manager) Dispose() {
	m.clientsLock.Lock()
	defer m.clientsLock.Unlock()

	for _, client := range m.clients {
		client.Dispose()
	}
}

// receive WeChat tcp package
func (m *Manager) Serve() {
	addr := fmt.Sprintf("127.0.0.1:%d", m.config.Wechat.ListenPort)
	log.Infof("Manager starting to listen on %s", addr)

	listen, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}

	for {
		conn, err := listen.Accept()
		if err != nil {
			log.Fatalf("Failed to accept: %v", err)
		}

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
					log.Warnf("Failed to unmarshal data from WeChat: %v", err)
					conn.Write([]byte("500 ERROR"))
				} else {
					go func() {
						m.mutex.LockKey(msg.Sender)
						defer m.mutex.UnlockKey(msg.Sender)

						if mxid, ok := m.pids[msg.PID]; ok {
							m.processFunc(mxid, &msg)
						} else {
							log.Warnf("Failed to map pid (%d) to remote mxid", msg.PID)
						}
					}()
					conn.Write([]byte("200 OK"))
				}
			}
		}(conn)
	}
}

func (m *Manager) GetClient(mxid string) *Client {
	m.clientsLock.Lock()
	client, ok := m.clients[mxid]
	m.clientsLock.Unlock()

	if ok {
		return client
	} else {
		return nil
	}
}

func (m *Manager) call(mxid string, f func(*Client, ...any) (any, error), v ...any) (any, error) {
	m.clientsLock.Lock()
	client, ok := m.clients[mxid]
	m.clientsLock.Unlock()

	if !ok {
		return nil, fmt.Errorf("client not found")
	} else {
		return f(client, v...)
	}
}

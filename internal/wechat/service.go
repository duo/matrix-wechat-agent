package wechat

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/duo/matrix-wechat-agent/internal/common"

	"github.com/duo/wsc"
	"github.com/tidwall/tinylru"

	log "github.com/sirupsen/logrus"
)

type Service struct {
	config *common.Configure

	workdir string
	docdir  string

	bridge  *wsc.Client
	manager *Manager

	history tinylru.LRU
}

func (s *Service) Start() {
	if err := s.bridge.Connect(); err != nil {
		log.Fatal(err)
	}

	go s.manager.Serve()
}

func (s *Service) Stop() {
	s.manager.Dispose()

	s.bridge.Disconnect()
}

func NewService(config *common.Configure) *Service {
	options, err := wsc.NewClientOptions(
		config.Service.Addr,
		wsc.HTTPHeaders(http.Header{
			"Authorization": []string{fmt.Sprintf("Basic %s", config.Service.Secret)},
		}),
		wsc.PingTimeout(config.Service.PingInterval),
	)
	if err != nil {
		log.Fatal(err)
	}

	workdir := filepath.Join(getDocDir(), "matrix_wechat_agent")
	if !pathExists(workdir) {
		if err := os.MkdirAll(workdir, 0o644); err != nil {
			log.Fatalf("Failed to create temp folder: %v", err)
		}
	}
	config.Wechat.Workdir = workdir

	service := &Service{
		config:  config,
		workdir: workdir,
		docdir:  getWechatDocdir(),
		bridge:  wsc.NewClient(options),
	}

	options.OnConnected = service.consumeWebsocket
	service.manager = NewManager(config, service.processWechatMessage)

	return service
}

// read messages from bridge
func (s *Service) consumeWebsocket(client *wsc.Client) {
	for {
		var msg common.Message
		err := s.bridge.ReadJSON(&msg)
		if err != nil {
			log.Debugln("Error reading from websocket:", err)
			return
		}

		switch msg.Type {
		case common.MsgRequest:
			request := msg.Data.(*common.Request)
			log.Debugf("Receive request #%d: %+v", msg.ID, request)
			go s.processRequest(msg.ID, msg.MXID, request)
		case common.MsgResponse:
			response := msg.Data.(*common.Response)
			log.Debugf("Receive response for #%d: %+v", msg.ID, response)
		}
	}
}

// process requests from bridge
func (s *Service) processRequest(id int64, mxid string, req *common.Request) {
	defer func() {
		panicErr := recover()
		if panicErr != nil {
			log.Errorf("Panic while responding to command %s in request #%d: %v\n%s", req.Type, id, panicErr, debug.Stack())
		}
	}()

	resp := s.actuallyHandleRequest(mxid, req)
	if id != 0 {
		respMsg := &common.Message{
			ID:   id,
			MXID: mxid,
			Type: common.MsgResponse,
			Data: resp,
		}
		log.Debugf("Send response for %d: %+v", id, respMsg)
		if err := s.bridge.WriteJSON(respMsg); err != nil {
			log.Warnf("Failed to send response for #%d: %v", id, err)
		}
	}
}

func (s *Service) actuallyHandleRequest(mxid string, req *common.Request) *common.Response {
	switch req.Type {
	case common.ReqEvent:
		ret, err := s.manager.SendMessage(mxid, req.Data.(*common.Event))
		return genResponse(common.RespEvent, ret, err)
	case common.ReqConnect:
		err := s.manager.Connect(mxid, s.workdir)
		return genResponse(common.RespConnect, nil, err)
	case common.ReqDisconnect:
		err := s.manager.Disconnet(mxid)
		return genResponse(common.RespDisconnect, nil, err)
	case common.ReqLoginQR:
		ret, err := s.manager.LoginWtihQRCode(mxid)
		return genResponse(common.RespLoginQR, ret, err)
	case common.ReqIsLogin:
		ret, err := s.manager.IsLogin(mxid)
		return genResponse(common.RespIsLogin, ret, err)
	case common.ReqGetSelf:
		ret, err := s.manager.GetSelf(mxid)
		return genResponse(common.RespGetSelf, ret, err)
	case common.ReqGetUserInfo:
		ret, err := s.manager.GetUserInfo(mxid, req.Data.([]string)[0])
		return genResponse(common.RespGetUserInfo, ret, err)
	case common.ReqGetGroupInfo:
		ret, err := s.manager.GetGroupInfo(mxid, req.Data.([]string)[0])
		return genResponse(common.RespGetGroupInfo, ret, err)
	case common.ReqGetGroupMembers:
		ret, err := s.manager.GetGroupMembers(mxid, req.Data.([]string)[0])
		return genResponse(common.RespGetGroupMembers, ret, err)
	case common.ReqGetGroupMemberNickname:
		ret, err := s.manager.GetGroupMemberNickname(mxid, req.Data.([]string)[0], req.Data.([]string)[1])
		return genResponse(common.RespGetGroupMemberNickname, ret, err)
	case common.ReqGetFriendList:
		ret, err := s.manager.GetFriendList(mxid)
		return genResponse(common.RespGetFriendList, ret, err)
	case common.ReqGetGroupList:
		ret, err := s.manager.GetGroupList(mxid)
		return genResponse(common.RespGetGroupList, ret, err)
	default:
		return nil
	}
}

// process WeChat message
func (s *Service) processWechatMessage(mxid string, msg *WechatMessage) {
	log.Debugf("Receive WeChat msg: %+v", msg)

	// Skip message sent by hook
	if msg.IsSendByPhone == 0 && msg.MsgType != 10000 {
		s.history.Set(msg.MsgID, struct{}{})
		return
	} else if _, ok := s.history.Get(msg.MsgID); ok {
		return
	}

	event := &common.Event{
		ID:        fmt.Sprint(msg.MsgID),
		Timestamp: msg.Timestamp * 1000,
		Type:      common.EventText,
		Content:   msg.Message,
		Chat:      common.Chat{ID: msg.Sender},
	}

	if msg.IsSendMsg == 0 {
		event.From = common.User{ID: msg.WxID}
		if !strings.HasSuffix(msg.Sender, "@chatroom") {
			event.Chat = common.Chat{ID: msg.Self}
		}
	} else {
		event.From = common.User{ID: msg.Self}
	}

	switch msg.MsgType {
	case 0: // unknown
		return
	case 1: // Txt
		event.Mentions = getMentions(s, msg)
	case 3: // Image
		if len(msg.FilePath) == 0 {
			return
		}
		blob := downloadImage(s, msg)
		if blob != nil {
			event.Type = common.EventPhoto
			event.Data = []*common.BlobData{blob}
		} else {
			event.Content = "[图片下载失败]"
		}
	case 34: // Voice
		blob := downloadVoice(s, msg, s.manager.GetClient(mxid))
		if blob != nil {
			event.Type = common.EventAudio
			event.Data = blob
		} else {
			event.Content = "[语音下载失败]"
		}
	case 42: // Card
		if card := parseCard(s, msg); card != nil {
			event.Type = common.EventApp
			event.Data = card
		} else {
			event.Content = "[名片解析失败]"
		}
	case 43: // Video
		if len(msg.FilePath) == 0 && len(msg.Thumbnail) == 0 {
			return
		}
		if _, ok := s.history.Get(msg.MsgID); ok {
			return
		}
		s.history.Set(msg.MsgID, struct{}{})

		blob := downloadVideo(s, msg)
		if blob != nil {
			event.Type = common.EventVideo
			event.Data = blob
		} else {
			event.Content = "[视频下载失败]"
		}
	case 47: // Sticker
		blob := downloadSticker(s, msg)
		if blob != nil {
			event.Type = common.EventPhoto
			event.Data = []*common.BlobData{blob}
		} else {
			event.Content = "[表情下载失败]"
		}
	case 48: // Location
		location := parseLocation(s, msg)
		if location != nil {
			event.Type = common.EventLocation
			event.Data = location
		} else {
			event.Content = "[位置解析失败]"
		}
	case 49: // App
		appType := getAppType(s, msg)
		switch appType {
		case 6: // File
			if len(msg.FilePath) == 0 {
				return
			}
			// FIXME: skip duplicate file
			if !strings.HasPrefix(msg.Message, "<?xml") {
				return
			}
			if _, ok := s.history.Set(msg.MsgID, struct{}{}); ok {
				return
			}
			blob := downloadFile(s, msg)
			if blob != nil {
				event.Type = common.EventFile
				event.Data = blob
			} else {
				event.Content = "[文件下载失败]"
			}
		case 8:
			if len(msg.FilePath) == 0 {
				return
			}
			// FIXME: skip duplicate sticker
			if strings.HasPrefix(msg.Message, "<?xml") {
				return
			}
			blob := downloadSticker(s, msg)
			if blob != nil {
				event.Type = common.EventPhoto
				event.Data = []*common.BlobData{blob}
			} else {
				event.Content = "[表情下载失败]"
			}
		case 57: // TODO: reply meesage not found fallback
			content, reply := parseReply(s, msg)
			if len(content) > 0 && reply != nil {
				event.Content = content
				event.Reply = reply
			}
		case 87:
			content := parseNotice(s, msg)
			if len(content) > 0 {
				event.Type = common.EventNotice
				event.Content = content
			}
		//case 2000: // Transfer
		default:
			app := parseApp(s, msg, appType)
			if app != nil {
				event.Type = common.EventApp
				event.Data = app
			} else {
				event.Content = "[应用解析失败]"
			}
		}
	case 50: // private voip
		event.Type = common.EventVoIP
		event.Content = parsePrivateVoIP(s, msg)
		if event.Content == "" {
			return
		}
	case 51: // last message
		return
	case 10000: // revoke
		content := parseRevoke(s, msg)
		if len(content) > 0 {
			event.Reply = &common.ReplyInfo{
				ID: event.ID,
			}
			event.ID = fmt.Sprint(time.Now().UnixMilli())
			event.Type = common.EventRevoke
			event.Content = content
		} else {
			event.Type = common.EventSystem
		}
	case 10002: // system
		if msg.Sender == "weixin" || msg.IsSendMsg == 1 {
			return
		}
		event.Type = common.EventSystem
		event.Content = parseSystemMessage(s, msg)
		if len(event.Content) == 0 {
			return
		}
		if event.Content == "You recalled a message" || event.Content == "你撤回了一条消息" {
			event.From = common.User{ID: msg.Self}
			if !strings.HasSuffix(msg.Sender, "@chatroom") {
				event.Chat = common.Chat{ID: msg.WxID}
			}
		}
	}

	s.pushEvent(mxid, event)
}

// push event ro bridge
func (s *Service) pushEvent(mxid string, event *common.Event) {
	msg := &common.Message{
		MXID: mxid,
		Type: common.MsgRequest,
		Data: &common.Request{
			Type: common.ReqEvent,
			Data: event,
		},
	}

	go func() {
		log.Debugf("Push event: %+v", event)
		if err := s.bridge.WriteJSON(msg); err != nil {
			log.Warnf("Failed to push event %s: %v", event.ID, err)
		}
	}()
}

func genResponse(rType common.ResponseType, data any, err error) *common.Response {
	resp := &common.Response{
		Type: rType,
	}

	if err != nil {
		resp.Error = &common.ErrorResponse{
			Code:    "PROCESS_FAILED",
			Message: err.Error(),
		}
	} else {
		resp.Data = data
	}

	return resp
}

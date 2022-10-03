package internal

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tidwall/tinylru"

	log "github.com/sirupsen/logrus"
)

const (
	ServerShuttingDown WebsocketCloseCode = "server_shutting_down"
	ConnectionReplaced WebsocketCloseCode = "conn_replaced"
)

var (
	ErrWebsocketManualStop   = errors.New("the websocket was disconnected manually")
	ErrWebsocketOverridden   = errors.New("a new call to StartWebsocket overrode the previous connection")
	ErrWebsocketUnknownError = errors.New("an unknown error occurred")

	ErrWebsocketNotConnected = errors.New("websocket not connected")
	ErrWebsocketClosed       = errors.New("websocket closed before response received")
)

// Native ErrorCodes
const (
	ErrUnknownToken ErrorCode = "M_UNKNOWN_TOKEN"
	ErrBadJSON      ErrorCode = "M_BAD_JSON"
	ErrNotJSON      ErrorCode = "M_NOT_JSON"
	ErrUnknown      ErrorCode = "M_UNKNOWN"
)

type AppService struct {
	Workdir string
	Docdir  string

	cache tinylru.LRU

	ws            *websocket.Conn
	wsWriteLock   sync.Mutex
	StopWebsocket func(error)
}

func (as *AppService) StartWebsocket(url, secret string, onConnect func()) error {
	ws, resp, err := websocket.DefaultDialer.Dial(url, http.Header{
		"Authorization": []string{fmt.Sprintf("Basic %s", secret)},
	})
	if resp != nil && resp.StatusCode >= 400 {
		var errResp Error
		err = json.NewDecoder(resp.Body).Decode(&errResp)
		if err != nil {
			return fmt.Errorf("websocket request returned HTTP %d with non-JSON body", resp.StatusCode)
		} else {
			return fmt.Errorf("websocket request returned %s (HTTP %d): %s", errResp.ErrorCode, resp.StatusCode, errResp.Message)
		}
	} else if err != nil {
		return fmt.Errorf("failed to open websocket: %w", err)
	}

	if as.StopWebsocket != nil {
		as.StopWebsocket(ErrWebsocketOverridden)
	}

	closeChan := make(chan error)
	closeChanOnce := sync.Once{}
	stopFunc := func(err error) {
		closeChanOnce.Do(func() {
			closeChan <- err
		})
	}

	as.ws = ws
	as.StopWebsocket = stopFunc
	log.Infoln("Appservice websocket connected")

	go as.consumeWebsocket(stopFunc, ws)

	if onConnect != nil {
		onConnect()
	}

	closeErr := <-closeChan

	if as.ws == ws {
		as.ws = nil
	}

	_ = ws.SetWriteDeadline(time.Now().Add(3 * time.Second))
	err = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, ""))
	if err != nil && !errors.Is(err, websocket.ErrCloseSent) {
		log.Warnln("Error writing close message to websocket:", err)
	}
	err = ws.Close()
	if err != nil {
		log.Warnln("Error closing websocket:", err)
	}
	return closeErr
}

func (as *AppService) consumeWebsocket(stopFunc func(error), ws *websocket.Conn) {
	defer stopFunc(ErrWebsocketUnknownError)
	for {
		var msg WebsocketMessage
		err := ws.ReadJSON(&msg)
		if err != nil {
			log.Debugln("Error reading from websocket:", err)
			stopFunc(parseCloseError(err))
			return
		}

		log.Debugf("Receive appserivde message: %+v", msg)

		if msg.Command == "" {
			continue
		}

		go as.handleCommand(&msg)
	}
}

func (as *AppService) handleCommand(msg *WebsocketMessage) {
	defer func() {
		panicErr := recover()
		if panicErr != nil {
			log.Errorf("Panic while responding to command %s in request #%d: %v\n%s", msg.Command, msg.ReqID, panicErr, debug.Stack())
		}
	}()
	ws := as.ws

	resp, err := actuallyHandleCommand(as, msg)

	if msg.ReqID != 0 {
		respPayload := &WebsocketCommandRequest{
			MXID:    msg.MXID,
			ReqID:   msg.ReqID,
			Command: CommandResponse,
			Data:    resp,
		}
		if err != nil {
			respPayload.Command = CommandError
			respPayload.Data = map[string]interface{}{
				"message": err.Error(),
			}
		}
		as.wsWriteLock.Lock()
		log.Debugf("Sending command response %+v", respPayload)
		err = ws.WriteJSON(&respPayload)
		as.wsWriteLock.Unlock()
		if err != nil {
			log.Warnf("Failed to send response to req #%d: %v", msg.ReqID, err)
		}
	}
}

func actuallyHandleCommand(as *AppService, msg *WebsocketMessage) (resp interface{}, err error) {
	defer func() {
		panicErr := recover()
		if panicErr != nil {
			log.Errorf("Panic while handling command %s in request #%d: %v\n%s", msg.Command, msg.ReqID, panicErr, debug.Stack())
			if err == nil {
				err = fmt.Errorf("internal server error")
			}
		}
	}()

	switch msg.Command {
	case CommandConnect:
		err = GetWechatManager().Connect(msg.MXID, as.Workdir)
	case CommandDisconnect:
		err = GetWechatManager().Disconnet(msg.MXID)
	case CommandLoginWithQRCode:
		resp, err = GetWechatManager().LoginWtihQRCode(msg.MXID)
	case CommandIsLogin:
		resp, err = GetWechatManager().IsLogin(msg.MXID)
	case CommandGetSelf:
		resp, err = GetWechatManager().GetSelf(msg.MXID)
	case CommandGetUserInfo:
		var data QueryData
		err = json.Unmarshal(msg.Data, &data)
		if err == nil {
			resp, err = GetWechatManager().GetUserInfo(msg.MXID, data.ID)
		}
	case CommandGetGroupInfo:
		var data QueryData
		err = json.Unmarshal(msg.Data, &data)
		if err == nil {
			resp, err = GetWechatManager().GetGroupInfo(msg.MXID, data.ID)
		}
	case CommandGetGroupMembers:
		var data QueryData
		err = json.Unmarshal(msg.Data, &data)
		if err == nil {
			resp, err = GetWechatManager().GetGroupMembers(msg.MXID, data.ID)
		}
	case CommandGetGroupMemberNickname:
		var data QueryData
		err = json.Unmarshal(msg.Data, &data)
		if err == nil {
			resp, err = GetWechatManager().GetGroupMemberNickname(msg.MXID, data.Group, data.ID)
		}
	case CommandGetFriendList:
		resp, err = GetWechatManager().GetFriendList(msg.MXID)
	case CommandGetGroupList:
		resp, err = GetWechatManager().GetGroupList(msg.MXID)
	case CommandSendMessage:
		var data MatrixMessage
		err = json.Unmarshal(msg.Data, &data)
		if err == nil {
			err = GetWechatManager().SendMessage(msg.MXID, &data)
		}
	}

	return
}

func (as *AppService) handleWechatMessage(mxid string, msg *WechatMessage) {
	defer func() {
		panicErr := recover()
		if panicErr != nil {
			log.Errorf("Panic while responding to wechat message: %v\n%s", panicErr, debug.Stack())
		}
	}()
	ws := as.ws

	// Skip message sent by hook
	if msg.IsSendByPhone == 0 {
		as.cache.Set(msg.MsgID, true)
		return
	} else if _, ok := as.cache.Get(msg.MsgID); ok {
		return
	}

	log.Debugln("Handle wechat message: %+v", msg)

	var ts int64
	if msg.Timestamp > 0 {
		ts = msg.Timestamp * 1000
	} else {
		tm, err := time.ParseInLocation("2006-01-02 15:04:05", msg.Time, time.Local)
		if err != nil {
			log.Warnf("Failed to parse time %s: %v", msg.Time, err)
			return
		}
		ts = tm.UnixMilli()
	}
	event := &WebsocketEventReqeust{
		MXID:      mxid,
		ID:        msg.MsgID,
		Timestamp: ts,
		Target:    msg.Sender,
		EventType: EventText,
		Content:   msg.Message,
	}
	if msg.IsSendMsg == 0 {
		event.Sender = msg.WxID
		if !strings.HasSuffix(msg.Sender, "@chatroom") {
			event.Target = msg.Self
		}
	} else {
		event.Sender = msg.Self
	}

	switch msg.MsgType {
	case 1: // Txt
		mentions := getMentions(as, msg)
		if mentions != nil {
			event.Extra = mentions
		}
	case 3: // Image
		if len(msg.FilePath) == 0 {
			return
		}
		blob := downloadImage(as, msg)
		if blob != nil {
			event.EventType = EventImage
			event.Extra = blob
		} else {
			event.Content = "[图片下载失败]"
		}
	case 34: // Voice
		blob := downloadVoice(as, msg)
		if blob != nil {
			event.EventType = EventAudio
			event.Extra = blob
		} else {
			event.Content = "[语音下载失败]"
		}
	case 43: // Video
		if len(msg.FilePath) == 0 && len(msg.Thumbnail) == 0 {
			return
		}
		blob := downloadVideo(as, msg)
		if blob != nil {
			event.EventType = EventVideo
			event.Extra = blob
		} else {
			event.Content = "[视频下载失败]"
		}
	case 47: // Sticker
		blob := downloadSticker(as, msg)
		if blob != nil {
			event.EventType = EventImage
			event.Extra = blob
		} else {
			event.Content = "[表情下载失败]"
		}
	case 48: // Location
		location := parseLocation(as, msg)
		if location != nil {
			event.EventType = EventLocation
			event.Extra = location
		} else {
			event.Content = "[位置解析失败]"
		}
	case 49: // App
		appType := getAppType(as, msg)
		switch appType {
		case 6: // File
			if len(msg.FilePath) == 0 {
				return
			}
			// FIXME: skip duplicate file
			if !strings.HasPrefix(msg.Message, "<?xml") {
				return
			}
			if _, ok := as.cache.Set(msg.MsgID, true); ok {
				return
			}
			blob := downloadFile(as, msg)
			if blob != nil {
				event.EventType = EventFile
				event.Extra = blob
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
			blob := downloadSticker(as, msg)
			if blob != nil {
				event.EventType = EventImage
				event.Extra = blob
			} else {
				event.Content = "[表情下载失败]"
			}
		case 57: // TODO: reply meesage not found fallback
			content, reply := parseReply(as, msg)
			if len(content) > 0 && reply != nil {
				event.Content = content
				event.Reply = *reply
			}
		case 87:
			content := parseNotice(as, msg)
			if len(content) > 0 {
				event.EventType = EventNotice
				event.Content = content
			}
		default:
			link := parseApp(as, msg)
			if link != nil {
				event.EventType = EventApp
				event.Extra = link
			} else {
				event.Content = "[应用解析失败]"
			}
		}
	case 10000: // revoke
		content := parseRevoke(as, msg)
		if len(content) > 0 {
			event.EventType = EventRevoke
			event.Content = content
		}
	}

	as.wsWriteLock.Lock()
	log.Debugf("Sending evnet %+v", event)
	err := ws.WriteJSON(&event)
	as.wsWriteLock.Unlock()
	if err != nil {
		log.Warnf("Failed to send event %d: %v", msg.MsgID, err)
	}
}

func (as *AppService) SendPing() error {
	ws := as.ws

	if ws == nil {
		return ErrWebsocketNotConnected
	}

	as.wsWriteLock.Lock()
	defer as.wsWriteLock.Unlock()
	_ = ws.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return ws.WriteJSON(&WebsocketCommand{Command: CommandPing})
}

func (as *AppService) SendWebsocket(msg *WebsocketMessage) error {
	ws := as.ws

	if msg == nil {
		return nil
	} else if ws == nil {
		return ErrWebsocketNotConnected
	}

	as.wsWriteLock.Lock()
	defer as.wsWriteLock.Unlock()
	_ = ws.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return ws.WriteJSON(msg)
}

func (as *AppService) HasWebsocket() bool {
	return as.ws != nil
}

type CloseCommand struct {
	Code    int                `json:"-"`
	Command string             `json:"command"`
	Status  WebsocketCloseCode `json:"status"`
}

func (cc CloseCommand) Error() string {
	return fmt.Sprintf("websocket: close %d: %s", cc.Code, cc.Status.String())
}

func parseCloseError(err error) error {
	closeError := &websocket.CloseError{}
	if !errors.As(err, &closeError) {
		return err
	}
	var closeCommand CloseCommand
	closeCommand.Code = closeError.Code
	closeCommand.Command = CommandDisconnect
	if len(closeError.Text) > 0 {
		jsonErr := json.Unmarshal([]byte(closeError.Text), &closeCommand)
		if jsonErr != nil {
			return err
		}
	}
	if len(closeCommand.Status) == 0 {
		if closeCommand.Code == 4001 {
			closeCommand.Status = ConnectionReplaced
		} else if closeCommand.Code == websocket.CloseServiceRestart {
			closeCommand.Status = ServerShuttingDown
		}
	}
	return &closeCommand
}

type WebsocketCloseCode string

func (c WebsocketCloseCode) String() string {
	switch c {
	case ServerShuttingDown:
		return "the server is shutting down"
	case ConnectionReplaced:
		return "the connection was replaced by another client"
	default:
		return string(c)
	}
}

// Error represents a Matrix protocol error.
type Error struct {
	HTTPStatus int       `json:"-"`
	ErrorCode  ErrorCode `json:"errcode"`
	Message    string    `json:"error"`
}

func (err Error) Write(w http.ResponseWriter) {
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(err.HTTPStatus)
	_ = Respond(w, &err)
}

// Respond responds to a HTTP request with a JSON object.
func Respond(w http.ResponseWriter, data interface{}) error {
	w.Header().Add("Content-Type", "application/json")
	dataStr, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = w.Write(dataStr)
	return err
}

// ErrorCode is the machine-readable code in an Error.
type ErrorCode string

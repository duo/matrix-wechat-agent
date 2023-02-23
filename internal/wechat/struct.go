package wechat

import "github.com/duo/matrix-wechat-agent/internal/common"

type WxIsLoginResp struct {
	IsLogin int    `json:"is_login"`
	Result  string `json:"result"`
}

type WxGetQRCodeResp struct {
	Message string `json:"msg"`
	Result  string `json:"result"`
}

type WxGetSelfResp struct {
	Data   WxUserInfo `json:"data"`
	Result string     `json:"result"`
}

type WxGetFriendListResp struct {
	Data   []*WxUserInfo `json:"data"`
	Result string        `json:"result"`
}

type WxGetGroupListResp struct {
	Data   []*WxGroupInfo `json:"data"`
	Result string         `json:"result"`
}

type WxGetGroupMembersResp struct {
	Members string `json:"members"`
	Result  string `json:"result"`
}

type WxContactResp struct {
	Data   [][5]string `json:"data,omitempty"`
	Result string      `json:"result"`
}

type WxUserInfo struct {
	ID        string `json:"wxId"`
	Nickname  string `json:"wxNickName"`
	BigAvatar string `json:"wxBigAvatar"`
	Remark    string `json:"wxRemark"`
}

func (w *WxUserInfo) toUserInfo() *common.UserInfo {
	if w == nil {
		return nil
	}

	return &common.UserInfo{
		ID:     w.ID,
		Name:   w.Nickname,
		Avatar: w.BigAvatar,
		Remark: w.Remark,
	}
}

type WxGroupInfo struct {
	ID        string   `json:"wxId"`
	Name      string   `json:"wxNickName"`
	BigAvatar string   `json:"wxBigAvatar"`
	Notice    string   `json:"notice"`
	Members   []string `json:"members"`
}

func (w *WxGroupInfo) toGroupInfo() *common.GroupInfo {
	if w == nil {
		return nil
	}

	return &common.GroupInfo{
		ID:      w.ID,
		Name:    w.Name,
		Avatar:  w.BigAvatar,
		Notice:  w.Notice,
		Members: w.Members,
	}
}

type WechatMessage struct {
	PID           int    `json:"pid"`
	MsgID         uint64 `json:"msgid"`
	Time          string `json:"time"`
	Timestamp     int64  `json:"timestamp"`
	WxID          string `json:"wxid"`
	Sender        string `json:"sender"`
	Self          string `json:"self"`
	IsSendMsg     int8   `json:"isSendMsg"`
	IsSendByPhone int8   `json:"isSendByPhone"`
	MsgType       int    `json:"type"`
	Message       string `json:"message"`
	FilePath      string `json:"filepath"`
	Thumbnail     string `json:"thumb_path"`
	ExtraInfo     string `json:"extrainfo"`
}

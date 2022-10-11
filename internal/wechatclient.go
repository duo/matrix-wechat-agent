package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/process"
	"github.com/tidwall/gjson"

	log "github.com/sirupsen/logrus"
)

const (
	CLIENT_API_URL = "http://127.0.0.1:%d/api/?type=%d"

	WECHAT_IS_LOGIN                     = 0
	WECHAT_GET_SELF_INFO                = 1
	WECHAT_MSG_SEND_TEXT                = 2
	WECHAT_MSG_SEND_AT                  = 3
	WECHAT_MSG_SEND_IMAGE               = 5
	WECHAT_MSG_SEND_FILE                = 6
	WECHAT_MSG_START_HOOK               = 9
	WECHAT_MSG_START_IMAGE_HOOK         = 11
	WECHAT_MSG_START_VOICE_HOOK         = 13
	WECHAT_CONTACT_GET_LIST             = 15
	WECHAT_CHATROOM_GET_MEMBER_LIST     = 25
	WECHAT_CHATROOM_GET_MEMBER_NICKNAME = 26
	WECHAT_DATABASE_GET_HANDLES         = 32
	WECHAT_DATABASE_QUERY               = 34
	WECHAT_GET_QROCDE_IMAGE             = 41
	WECHAT_LOGOUT                       = 44

	DB_MICRO_MSG      = "MicroMsg.db"
	DB_OPENIM_CONTACT = "OpenIMContact.db"
)

type WechatClient struct {
	port int32
	pid  uintptr
	proc *process.Process
}

func (c *WechatClient) IsAlive() bool {
	status, err := c.proc.IsRunning()
	if err != nil {
		return false
	}
	return status
}

func (c *WechatClient) Dispose() error {
	if c.proc == nil {
		return nil
	}
	ok, err := c.proc.IsRunning()
	if err != nil {
		return err
	}

	c.Logout()

	if ok {
		children, err := c.proc.Children()
		if err != nil {
			return err
		}
		for _, v := range children {
			if err := v.Kill(); err != nil {
				return err
			}
		}
		if err := c.proc.Kill(); err != nil {
			return err
		}
	}

	return nil
}

func (c *WechatClient) HookMsg(savePath string) error {
	path, err := json.Marshal(map[string]string{
		"save_path": savePath,
	})
	if err != nil {
		return err
	}

	_, err = post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_MSG_START_HOOK),
		[]byte(fmt.Sprintf(`{"port":%d}`, listenPort)),
	)
	if err != nil {
		return err
	}
	_, err = post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_MSG_START_IMAGE_HOOK),
		path,
	)
	if err != nil {
		return err
	}
	_, err = post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_MSG_START_VOICE_HOOK),
		path,
	)
	if err != nil {
		return err
	}

	return nil
}

func (c *WechatClient) LoginWtihQRCode() ([]byte, error) {
	// FIXME: skip the first qr code
	time.Sleep(3 * time.Second)

	ret, err := post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_GET_QROCDE_IMAGE),
		[]byte("{}"),
	)
	if err != nil {
		return nil, err
	}

	var resp GetQRCodeResp
	err = json.Unmarshal(ret, &resp)
	if err != nil {
		return ret, nil
	} else {
		return nil, fmt.Errorf("%v", resp.Message)
	}
}

func (c *WechatClient) Logout() error {
	_, err := post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_LOGOUT),
		[]byte("{}"),
	)

	return err
}

func (c *WechatClient) IsLogin() bool {
	ret, err := post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_IS_LOGIN),
		[]byte("{}"),
	)
	if err != nil {
		return false
	}

	var resp IsLoginResp
	err = json.Unmarshal(ret, &resp)
	if err != nil || resp.Result != "OK" {
		log.Warnln("Failed to parse is_login response", err)
		return false
	}

	return resp.IsLogin == 1
}

func (c *WechatClient) GetSelf() (*UserInfo, error) {
	if !c.IsLogin() {
		return nil, fmt.Errorf("user not logged")
	}

	ret, err := post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_GET_SELF_INFO),
		[]byte("{}"),
	)
	if err != nil {
		return nil, err
	}

	var resp GetSelfResp
	err = json.Unmarshal(ret, &resp)
	if err != nil || resp.Result != "OK" {
		log.Warnln("Failed to parse get_self response", err)
		return nil, err
	}

	return &resp.Data, nil
}

func (c *WechatClient) GetUserInfo(wxid string) (*UserInfo, error) {
	if !c.IsLogin() {
		return nil, fmt.Errorf("user not logged")
	}

	var handle int64
	var sql string
	var err error

	if strings.HasSuffix(wxid, "@openim") {
		handle, err = c.getDbHandleByName(DB_OPENIM_CONTACT)
		if err != nil {
			return nil, err
		}

		sql = fmt.Sprintf(`
			SELECT UserName, NickName, BigHeadImgUrl, SmallHeadImgUrl, Remark
			FROM OpenIMContact
			WHERE UserName="%s"
		`, wxid)
	} else {
		handle, err = c.getDbHandleByName(DB_MICRO_MSG)
		if err != nil {
			return nil, err
		}

		sql = fmt.Sprintf(`
			SELECT c.UserName, c.NickName, i.bigHeadImgUrl, i.smallHeadImgUrl, c.Remark
			FROM Contact AS c
			LEFT JOIN ContactHeadImgUrl AS i
				ON c.UserName = i.usrName
			WHERE c.UserName="%s"
		`, wxid)
	}

	jsonSql, err := json.Marshal(map[string]interface{}{
		"db_handle": handle,
		"sql":       sql,
	})
	if err != nil {
		return nil, err
	}

	ret, err := post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_DATABASE_QUERY),
		jsonSql,
	)
	if err != nil {
		return nil, err
	}

	if gjson.GetBytes(ret, "data.#").Int() <= 1 {
		return nil, fmt.Errorf("user %s not found", wxid)
	}

	info := &UserInfo{
		ID:        gjson.GetBytes(ret, "data.1.0").String(),
		Nickname:  gjson.GetBytes(ret, "data.1.1").String(),
		BigAvatar: gjson.GetBytes(ret, "data.1.2").String(),
		Remark:    gjson.GetBytes(ret, "data.1.4").String(),
	}
	if len(info.BigAvatar) == 0 {
		info.BigAvatar = gjson.GetBytes(ret, "data.1.3").String()
	}

	return info, nil
}

func (c *WechatClient) GetGroupInfo(wxid string) (*GroupInfo, error) {
	if !c.IsLogin() {
		return nil, fmt.Errorf("user not logged")
	}

	handle, err := c.getDbHandleByName(DB_MICRO_MSG)
	if err != nil {
		return nil, err
	}

	sql := fmt.Sprintf(`
		SELECT c.UserName, c.NickName, i.bigHeadImgUrl, i.smallHeadImgUrl
		FROM Contact AS c
		LEFT JOIN ContactHeadImgUrl AS i
			ON c.UserName = i.usrName
		WHERE c.UserName="%s"
	`, wxid)

	jsonSql, err := json.Marshal(map[string]interface{}{
		"db_handle": handle,
		"sql":       sql,
	})
	if err != nil {
		return nil, err
	}

	ret, err := post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_DATABASE_QUERY),
		jsonSql,
	)
	if err != nil {
		return nil, err
	}

	if gjson.GetBytes(ret, "data.#").Int() <= 1 {
		return nil, fmt.Errorf("group %s not found", wxid)
	}

	info := &GroupInfo{
		ID:        gjson.GetBytes(ret, "data.1.0").String(),
		Name:      gjson.GetBytes(ret, "data.1.1").String(),
		BigAvatar: gjson.GetBytes(ret, "data.1.2").String(),
	}
	if len(info.BigAvatar) == 0 {
		info.BigAvatar = gjson.GetBytes(ret, "data.1.3").String()
	}

	sql = fmt.Sprintf(`SELECT Announcement FROM ChatRoomInfo WHERE ChatRoomName="%s"`, wxid)
	jsonSql, err = json.Marshal(map[string]interface{}{
		"db_handle": handle,
		"sql":       sql,
	})
	if err != nil {
		return nil, err
	}

	ret, err = post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_DATABASE_QUERY),
		jsonSql,
	)
	if err != nil {
		return nil, err
	}

	if gjson.GetBytes(ret, "data.#").Int() <= 1 {
		return nil, fmt.Errorf("group %s not found", wxid)
	}
	info.Notice = gjson.GetBytes(ret, "data.1.0").String()

	return info, nil
}

func (c *WechatClient) GetGroupMembers(wxid string) ([]string, error) {
	if !c.IsLogin() {
		return nil, fmt.Errorf("user not logged")
	}

	ret, err := post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_CHATROOM_GET_MEMBER_LIST),
		[]byte(fmt.Sprintf(`{"chatroom_id":"%s"}`, wxid)),
	)
	if err != nil {
		return nil, err
	}

	var resp GetGroupMembersResp
	err = json.Unmarshal(ret, &resp)
	if err != nil || resp.Result != "OK" {
		log.Warnln("Failed to parse get_group_members response", err)
		return nil, err
	}

	return strings.Split(resp.Members, "^G"), nil
}

func (c *WechatClient) GetGroupMemberNickname(group, wxid string) (string, error) {
	if !c.IsLogin() {
		return "", fmt.Errorf("user not logged")
	}

	ret, err := post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_CHATROOM_GET_MEMBER_NICKNAME),
		[]byte(fmt.Sprintf(`{"chatroom_id":"%s", "wxid":"%s"}`, group, wxid)),
	)
	if err != nil {
		return "", err
	}

	return gjson.GetBytes(ret, "nickname").String(), nil
}

func (c *WechatClient) GetFriendList() ([]*UserInfo, error) {
	if !c.IsLogin() {
		return nil, fmt.Errorf("user not logged")
	}

	contacts, err := c.GetContacts()
	if err != nil {
		return nil, err
	}

	var friends []*UserInfo
	for _, c := range contacts {
		if !strings.HasSuffix(c[0], "@chatroom") {
			info := &UserInfo{
				ID:        c[0],
				Nickname:  c[1],
				BigAvatar: c[2],
				Remark:    c[4],
			}
			if len(info.BigAvatar) == 0 {
				info.BigAvatar = c[3]
			}

			friends = append(friends, info)
		}
	}

	openIMContacts, err := c.GetOpenIMContacts()
	if err == nil {
		for _, c := range openIMContacts {
			if !strings.HasSuffix(c[0], "@chatroom") {
				info := &UserInfo{
					ID:        c[0],
					Nickname:  c[1],
					BigAvatar: c[2],
					Remark:    c[4],
				}
				if len(info.BigAvatar) == 0 {
					info.BigAvatar = c[3]
				}

				friends = append(friends, info)
			}
		}
	}

	return friends, nil
}

func (c *WechatClient) GetGroupList() ([]*GroupInfo, error) {
	if !c.IsLogin() {
		return nil, fmt.Errorf("user not logged")
	}

	contacts, err := c.GetContacts()
	if err != nil {
		return nil, err
	}

	var groups []*GroupInfo
	for _, c := range contacts {
		if strings.HasSuffix(c[0], "@chatroom") {
			info := &GroupInfo{
				ID:        c[0],
				Name:      c[1],
				BigAvatar: c[2],
			}
			if len(info.BigAvatar) == 0 {
				info.BigAvatar = c[3]
			}

			groups = append(groups, info)
		}
	}

	return groups, nil
}

func (c *WechatClient) SendText(target string, content string) error {
	data, err := json.Marshal(map[string]string{
		"wxid": target,
		"msg":  content,
	})
	if err != nil {
		return err
	}

	_, err = post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_MSG_SEND_TEXT),
		data,
	)
	if err != nil {
		return err
	}

	return nil
}

func (c *WechatClient) SendAtText(target string, content string, mentions []string) error {
	wxids := strings.Join(mentions, ",")
	data, err := json.Marshal(map[string]interface{}{
		"chatroom_id":   target,
		"msg":           content,
		"wxids":         wxids,
		"auto_nickname": 0,
	})

	if err != nil {
		return err
	}

	_, err = post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_MSG_SEND_AT),
		data,
	)
	if err != nil {
		return err
	}

	return nil
}

func (c *WechatClient) SendImage(target string, path string) error {
	data, err := json.Marshal(map[string]string{
		"receiver": target,
		"img_path": path,
	})
	if err != nil {
		return err
	}

	_, err = post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_MSG_SEND_IMAGE),
		data,
	)
	if err != nil {
		return err
	}

	return nil
}

func (c *WechatClient) SendFile(target string, path string) error {
	data, err := json.Marshal(map[string]string{
		"receiver":  target,
		"file_path": path,
	})
	if err != nil {
		return err
	}

	_, err = post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_MSG_SEND_FILE),
		data,
	)
	if err != nil {
		return err
	}

	return nil
}

func (c *WechatClient) GetOpenIMContacts() ([][5]string, error) {
	handle, err := c.getDbHandleByName(DB_OPENIM_CONTACT)
	if err != nil {
		return nil, err
	}

	sql := fmt.Sprintf(`
		SELECT UserName, NickName, BigHeadImgUrl, SmallHeadImgUrl, Remark
		FROM OpenIMContact
	`)

	jsonSql, err := json.Marshal(map[string]interface{}{
		"db_handle": handle,
		"sql":       sql,
	})
	if err != nil {
		return nil, err
	}

	ret, err := post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_DATABASE_QUERY),
		jsonSql,
	)
	if err != nil {
		return nil, err
	}

	if gjson.GetBytes(ret, "data.#").Int() <= 1 {
		return [][5]string{}, nil
	}

	var result ContactResp
	err = json.Unmarshal(ret, &result)
	if err != nil || result.Result != "OK" {
		log.Warnln("Failed to parse get contacts response", err)
		return nil, err
	}

	return result.Data[1:], nil
}

func (c *WechatClient) GetContacts() ([][5]string, error) {
	handle, err := c.getDbHandleByName(DB_MICRO_MSG)
	if err != nil {
		return nil, err
	}

	sql := fmt.Sprintf(`
		SELECT c.UserName, c.NickName, i.bigHeadImgUrl, i.smallHeadImgUrl, c.Remark
		FROM Contact AS c
		LEFT JOIN ContactHeadImgUrl AS i
			ON c.UserName = i.usrName
	`)

	jsonSql, err := json.Marshal(map[string]interface{}{
		"db_handle": handle,
		"sql":       sql,
	})
	if err != nil {
		return nil, err
	}

	ret, err := post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_DATABASE_QUERY),
		jsonSql,
	)
	if err != nil {
		return nil, err
	}

	if gjson.GetBytes(ret, "data.#").Int() <= 1 {
		return [][5]string{}, nil
	}

	var result ContactResp
	err = json.Unmarshal(ret, &result)
	if err != nil || result.Result != "OK" {
		log.Warnln("Failed to parse get contacts response", err)
		return nil, err
	}

	return result.Data[1:], nil
}

func (c *WechatClient) getDbHandleByName(name string) (int64, error) {
	if !c.IsLogin() {
		return 0, fmt.Errorf("user not logged")
	}

	ret, err := post(
		fmt.Sprintf(CLIENT_API_URL, c.port, WECHAT_DATABASE_GET_HANDLES),
		[]byte("{}"),
	)
	if err != nil {
		return 0, err
	}

	query := fmt.Sprintf(`data.#(db_name="%s").handle`, name)

	result := gjson.GetBytes(ret, query)
	if result.Exists() {
		return result.Int(), nil
	} else {
		return 0, fmt.Errorf("db %s not found", name)
	}
}

func post(url string, data []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

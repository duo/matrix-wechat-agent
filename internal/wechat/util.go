package wechat

import (
	"compress/gzip"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/duo/matrix-wechat-agent/internal/common"

	"github.com/antchfx/xmlquery"
	"golang.org/x/sys/windows/registry"
)

var (
	httpClient = &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2:   true,
			MaxConnsPerHost:     0,
			MaxIdleConns:        0,
			MaxIdleConnsPerHost: 256,
		},
	}

	UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/87.0.4280.88 Safari/537.36 Edg/87.0.664.66"
)

func LoadDriver() syscall.Handle {
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

	return driver
}

func getMentions(s *Service, msg *WechatMessage) []string {
	if len(msg.ExtraInfo) == 0 {
		return nil
	}

	doc, err := xmlquery.Parse(strings.NewReader(msg.ExtraInfo))
	if err != nil {
		return nil
	}

	atuserNode := xmlquery.FindOne(doc, "/msgsource/atuserlist")
	if atuserNode == nil || len(atuserNode.InnerText()) == 0 {
		return nil
	}

	return strings.FieldsFunc(strings.TrimSpace(atuserNode.InnerText()), func(r rune) bool {
		return r == ','
	})
}

func downloadImage(s *Service, msg *WechatMessage) *common.BlobData {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.Wechat.RequestTimeout)
	defer cancel()

	imageFile := filepath.Join(s.workdir, msg.Self, filepath.Base(msg.FilePath))

	baseFile := strings.TrimSuffix(imageFile, filepath.Ext(imageFile))
	fileName := filepath.Base(msg.FilePath)
	pngFile := baseFile + ".png"
	gifFile := baseFile + ".gif"
	jpgFile := baseFile + ".jpg"

	for {
		var data []byte
		var err error
		switch {
		case pathExists(baseFile):
			data, err = os.ReadFile(baseFile)
		case pathExists(pngFile):
			fileName = fileName + ".png"
			data, err = os.ReadFile(pngFile)
		case pathExists(gifFile):
			fileName = fileName + ".gif"
			data, err = os.ReadFile(gifFile)
		case pathExists(jpgFile):
			fileName = fileName + ".jpg"
			data, err = os.ReadFile(jpgFile)
		}

		if err == nil && data != nil {
			return &common.BlobData{
				Name:   fileName,
				Binary: data,
			}
		}

		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return nil
		}
	}
}

func downloadVoice(s *Service, msg *WechatMessage, client *Client) *common.BlobData {
	doc, err := xmlquery.Parse(strings.NewReader(msg.Message))
	if err != nil {
		return nil
	}

	node := xmlquery.FindOne(doc, "/msg/voicemsg/@clientmsgid")
	if node == nil || len(node.InnerText()) == 0 {
		return nil
	}
	path := node.InnerText()

	ctx, cancel := context.WithTimeout(context.Background(), s.config.Wechat.RequestTimeout)
	defer cancel()

	voiceFile := filepath.Join(s.workdir, msg.Self, path+".amr")
	for {
		// check from disk
		if pathExists(voiceFile) {
			data, err := os.ReadFile(voiceFile)
			if err == nil && data != nil {
				return &common.BlobData{
					Name:   filepath.Base(voiceFile),
					Binary: data,
				}
			}
		}

		// check from db
		if client != nil {
			if data, err := client.GetVoice(msg.MsgID); err != nil {
				return nil
			} else if data != nil {
				return &common.BlobData{
					Name:   path + ".amr",
					Binary: data,
				}
			}
		}

		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return nil
		}
	}
}

func downloadVideo(s *Service, msg *WechatMessage) *common.BlobData {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.Wechat.RequestTimeout)
	defer cancel()

	var videoFile string
	if len(msg.FilePath) > 0 {
		videoFile = filepath.Join(s.docdir, msg.FilePath)
	} else {
		videoFile = filepath.Join(s.docdir, msg.Thumbnail)
		videoFile = strings.TrimSuffix(videoFile, filepath.Ext(videoFile))
		videoFile += ".mp4"
	}
	for {
		if pathExists(videoFile) {
			data, err := os.ReadFile(videoFile)
			if err == nil && data != nil {
				return &common.BlobData{
					Name:   filepath.Base(videoFile),
					Binary: data,
				}
			}
		}

		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return nil
		}
	}
}

func downloadSticker(s *Service, msg *WechatMessage) *common.BlobData {
	doc, err := xmlquery.Parse(strings.NewReader(msg.Message))
	if err != nil {
		return nil
	}

	urlNode := xmlquery.FindOne(doc, "//@cdnurl")
	if urlNode == nil || len(urlNode.InnerText()) == 0 {
		return nil
	}
	url := urlNode.InnerText()
	hashNode := xmlquery.FindOne(doc, "//@aeskey")
	if hashNode == nil || len(hashNode.InnerText()) == 0 {
		return nil
	}
	hash := hashNode.InnerText()

	data, err := GetBytes(url)
	if err == nil {
		return &common.BlobData{
			Name:   hash,
			Binary: data,
		}
	} else {
		return nil
	}
}

func parseLocation(s *Service, msg *WechatMessage) *common.LocationData {
	doc, err := xmlquery.Parse(strings.NewReader(msg.Message))
	if err != nil {
		return nil
	}

	latitudeNode := xmlquery.FindOne(doc, "/msg/location/@x")
	if latitudeNode == nil || len(latitudeNode.InnerText()) == 0 {
		return nil
	}
	latitude, err := strconv.ParseFloat(latitudeNode.InnerText(), 64)
	if err != nil {
		return nil
	}
	longitudeNode := xmlquery.FindOne(doc, "/msg/location/@y")
	if longitudeNode == nil || len(longitudeNode.InnerText()) == 0 {
		return nil
	}
	longitude, err := strconv.ParseFloat(longitudeNode.InnerText(), 64)
	if err != nil {
		return nil
	}
	nameNode := xmlquery.FindOne(doc, "/msg/location/@poiname")
	var name string
	if nameNode != nil {
		name = nameNode.InnerText()
	}
	labelNode := xmlquery.FindOne(doc, "/msg/location/@label")
	var label string
	if labelNode != nil {
		label = labelNode.InnerText()
	}

	return &common.LocationData{
		Name:      name,
		Address:   label,
		Latitude:  latitude,
		Longitude: longitude,
	}
}

func getAppType(s *Service, msg *WechatMessage) int {
	doc, err := xmlquery.Parse(strings.NewReader(msg.Message))
	if err != nil {
		return 0
	}

	node := xmlquery.FindOne(doc, "/msg/appmsg/type")
	if node == nil || len(node.InnerText()) == 0 {
		return 0
	}

	appType, err := strconv.Atoi(node.InnerText())
	if err == nil {
		return appType
	}

	return 0
}

func parseReply(s *Service, msg *WechatMessage) (string, *common.ReplyInfo) {
	doc, err := xmlquery.Parse(strings.NewReader(msg.Message))
	if err != nil {
		return "", nil
	}

	titleNode := xmlquery.FindOne(doc, "/msg/appmsg/title")
	if titleNode == nil || len(titleNode.InnerText()) == 0 {
		return "", nil
	}

	svridNode := xmlquery.FindOne(doc, "/msg/appmsg/refermsg/svrid")
	if svridNode == nil || len(svridNode.InnerText()) == 0 {
		return "", nil
	}

	userNode := xmlquery.FindOne(doc, "/msg/appmsg/refermsg/chatusr")
	if userNode == nil || len(userNode.InnerText()) == 0 {
		userNode = xmlquery.FindOne(doc, "/msg/appmsg/refermsg/fromusr")
		if userNode == nil || len(userNode.InnerText()) == 0 {
			return "", nil
		}
	}

	msgId, err := strconv.ParseUint(svridNode.InnerText(), 10, 64)
	if err != nil {
		return "", nil
	}

	return titleNode.InnerText(), &common.ReplyInfo{ID: fmt.Sprint(msgId), Sender: userNode.InnerText()}
}

func parseNotice(s *Service, msg *WechatMessage) string {
	doc, err := xmlquery.Parse(strings.NewReader(msg.Message))
	if err != nil {
		return ""
	}

	noticeNode := xmlquery.FindOne(doc, "/msg/appmsg/textannouncement")
	if noticeNode == nil {
		return ""
	}

	return noticeNode.InnerText()
}

func parseCard(s *Service, msg *WechatMessage) *common.AppData {
	doc, err := xmlquery.Parse(strings.NewReader(msg.Message))
	if err != nil {
		return nil
	}

	node := xmlquery.FindOne(doc, "/msg")
	if node == nil {
		return nil
	}

	return &common.AppData{
		Title:       "",
		Description: node.SelectAttr("nickname"),
		Source:      node.SelectAttr("nickname"),
		URL:         node.SelectAttr("bigheadimgurl"),
	}
}

func parseApp(s *Service, msg *WechatMessage, appType int) *common.AppData {
	doc, err := xmlquery.Parse(strings.NewReader(msg.Message))
	if err != nil {
		return nil
	}

	switch appType {
	case 1:
		titleNode := xmlquery.FindOne(doc, "/msg/appmsg/title")
		if titleNode == nil || len(titleNode.InnerText()) == 0 {
			return nil
		}
		return &common.AppData{
			Title:       "",
			Description: titleNode.InnerText(),
			Source:      "",
			URL:         "",
		}
	case 19: // forward
		titleNode := xmlquery.FindOne(doc, "/msg/appmsg/title")
		if titleNode == nil || len(titleNode.InnerText()) == 0 {
			return nil
		}
		var des string
		desNode := xmlquery.FindOne(doc, "/msg/appmsg/des")
		if desNode != nil {
			des = desNode.InnerText()
		}
		return &common.AppData{
			Title:       titleNode.InnerText(),
			Description: des,
			Source:      "",
			URL:         "",
		}
	case 51: // video
		titleNode := xmlquery.FindOne(doc, "/msg/appmsg/finderFeed/nickname")
		if titleNode == nil || len(titleNode.InnerText()) == 0 {
			return nil
		}
		var des string
		desNode := xmlquery.FindOne(doc, "/msg/appmsg/finderFeed/desc")
		if desNode != nil {
			des = desNode.InnerText()
		}
		var url string
		urlNode := xmlquery.FindOne(doc, "/msg/appmsg/finderFeed//fullCoverUrl")
		if urlNode != nil {
			url = urlNode.InnerText()
		}
		return &common.AppData{
			Title:       titleNode.InnerText(),
			Description: des,
			Source:      titleNode.InnerText(),
			URL:         url,
		}
	case 63: // live
		titleNode := xmlquery.FindOne(doc, "/msg/appmsg/finderLive/nickname")
		if titleNode == nil || len(titleNode.InnerText()) == 0 {
			return nil
		}
		var des string
		desNode := xmlquery.FindOne(doc, "/msg/appmsg/finderLive/desc")
		if desNode != nil {
			des = desNode.InnerText()
		}
		var url string
		urlNode := xmlquery.FindOne(doc, "/msg/appmsg/finderLive//coverUrl")
		if urlNode != nil {
			url = urlNode.InnerText()
		}
		return &common.AppData{
			Title:       titleNode.InnerText(),
			Description: des,
			Source:      titleNode.InnerText(),
			URL:         url,
		}
	default:
		titleNode := xmlquery.FindOne(doc, "/msg/appmsg/title")
		if titleNode == nil || len(titleNode.InnerText()) == 0 {
			return nil
		}
		var url string
		urlNode := xmlquery.FindOne(doc, "/msg/appmsg/url")
		if urlNode != nil {
			url = urlNode.InnerText()
		}
		var des string
		desNode := xmlquery.FindOne(doc, "/msg/appmsg/des")
		if desNode != nil {
			des = desNode.InnerText()
		}
		var source string
		if sourceNode := xmlquery.FindOne(doc, "/msg/appmsg/sourcedisplayname"); sourceNode != nil {
			source = sourceNode.InnerText()
		} else if sourceNode := xmlquery.FindOne(doc, "/msg/appinfo/appname"); sourceNode != nil {
			source = sourceNode.InnerText()
		}
		return &common.AppData{
			Title:       titleNode.InnerText(),
			Description: des,
			Source:      source,
			URL:         url,
		}
	}
}

func parseRevoke(s *Service, msg *WechatMessage) string {
	doc, err := xmlquery.Parse(strings.NewReader(msg.Message))
	if err != nil {
		return ""
	}

	revokeNode := xmlquery.FindOne(doc, "/revokemsg")
	if revokeNode == nil {
		return ""
	}

	return revokeNode.InnerText()
}

func parsePrivateVoIP(s *Service, msg *WechatMessage) string {
	doc, err := xmlquery.Parse(strings.NewReader(msg.Message))
	if err != nil {
		return ""
	}

	inviteNode := xmlquery.FindOne(doc, "/voipinvitemsg")
	if inviteNode != nil {
		statusNode := xmlquery.FindOne(doc, "/voipinvitemsg/status")
		if statusNode != nil {
			switch statusNode.InnerText() {
			case "1":
				return "VoIP: Started a call"
			case "2":
				return "VoIP: Call ended"
			default:
				return fmt.Sprintf("VoIP: Unknown status %s", statusNode.InnerText())
			}
		}
	}
	bubbleNode := xmlquery.FindOne(doc, "/voipmsg")
	if bubbleNode != nil {
		msgNode := xmlquery.FindOne(doc, "//msg")
		if msgNode != nil {
			return fmt.Sprintf("VoIP: %s", msgNode.InnerText())
		}
	}

	return ""
}

func parseSystemMessage(s *Service, msg *WechatMessage) string {
	doc, err := xmlquery.Parse(strings.NewReader(msg.Message))
	if err != nil {
		return ""
	}

	// TODO:
	inviteNode := xmlquery.FindOne(doc, "/sysmsg/voipmt/invite")
	if inviteNode != nil {
		return fmt.Sprintf("VoIP: %s", inviteNode.InnerText())
	}
	bannerNode := xmlquery.FindOne(doc, "/sysmsg/voipmt/banner")
	if bannerNode != nil {
		return fmt.Sprintf("VoIP: %s", bannerNode.InnerText())
	}
	/*
		replaceNode := xmlquery.FindOne(doc, "/sysmsg/revokemsg/replacemsg")
		if replaceNode != nil {
			return replaceNode.InnerText()
		}
	*/

	return ""
}

func downloadFile(s *Service, msg *WechatMessage) *common.BlobData {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.Wechat.RequestTimeout)
	defer cancel()

	file := filepath.Join(s.docdir, msg.FilePath)
	for {
		if pathExists(file) {
			data, err := os.ReadFile(file)
			if err == nil && data != nil {
				return &common.BlobData{
					Name:   filepath.Base(file),
					Binary: data,
				}
			}
		}

		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return nil
		}
	}
}

func saveBlob(workdir string, msg *common.Event) string {
	var data *common.BlobData
	if msg.Type == common.EventPhoto {
		// TODO:
		data = msg.Data.([]*common.BlobData)[0]
	} else {
		data = msg.Data.(*common.BlobData)
	}

	var path string
	if len(data.Name) > 0 {
		path = filepath.Join(workdir, data.Name)
	} else {
		path = filepath.Join(workdir, fmt.Sprintf("%x", md5.Sum(data.Binary)))
	}

	if err := os.WriteFile(path, data.Binary, 0o644); err != nil {
		return ""
	}

	return path
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || errors.Is(err, os.ErrExist)
}

func getDocDir() string {
	u, _ := user.Current()
	baseDir := filepath.Join(u.HomeDir, "Documents")

	// Old windows path
	if !pathExists(baseDir) {
		baseDir = filepath.Join(u.HomeDir, "My Documents")
	}

	return baseDir
}

func getWechatDocdir() string {
	baseDir := getDocDir()

	regKey, err := registry.OpenKey(registry.CURRENT_USER, "SOFTWARE\\Tencent\\WeChat", registry.QUERY_VALUE)
	if err == nil {
		path, _, err := regKey.GetStringValue("FileSavePath")
		if err == nil && path != "MyDocument:" && path != "" {
			baseDir = path
		}
	}

	return filepath.Join(baseDir, "WeChat Files")
}

func GetBytes(url string) ([]byte, error) {
	reader, err := HTTPGetReadCloser(url)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = reader.Close()
	}()

	return io.ReadAll(reader)
}

type gzipCloser struct {
	f io.Closer
	r *gzip.Reader
}

func NewGzipReadCloser(reader io.ReadCloser) (io.ReadCloser, error) {
	gzipReader, err := gzip.NewReader(reader)
	if err != nil {
		return nil, err
	}

	return &gzipCloser{
		f: reader,
		r: gzipReader,
	}, nil
}

func (g *gzipCloser) Read(p []byte) (n int, err error) {
	return g.r.Read(p)
}

func (g *gzipCloser) Close() error {
	_ = g.f.Close()

	return g.r.Close()
}

func HTTPGetReadCloser(url string) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header["User-Agent"] = []string{UserAgent}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if strings.Contains(resp.Header.Get("Content-Encoding"), "gzip") {
		return NewGzipReadCloser(resp.Body)
	}

	return resp.Body, err
}

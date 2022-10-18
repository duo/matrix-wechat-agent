package internal

import (
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/antchfx/xmlquery"
	"golang.org/x/sys/windows/registry"
)

const MediaDownloadTiemout = 30 * time.Second

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

func getMentions(as *AppService, msg *WechatMessage) []string {
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

func downloadImage(as *AppService, msg *WechatMessage) *BlobData {
	ctx, cancel := context.WithTimeout(context.Background(), MediaDownloadTiemout)
	defer cancel()

	imageFile := filepath.Join(as.Workdir, msg.Self, filepath.Base(msg.FilePath))

	baseFile := strings.TrimSuffix(imageFile, filepath.Ext(imageFile))
	pngFile := baseFile + ".png"
	gifFile := baseFile + ".gif"
	jpgFile := baseFile + ".jpg"

	for {
		var data []byte
		var err error
		switch {
		case PathExists(baseFile):
			data, err = os.ReadFile(baseFile)
		case PathExists(pngFile):
			data, err = os.ReadFile(pngFile)
		case PathExists(gifFile):
			data, err = os.ReadFile(gifFile)
		case PathExists(jpgFile):
			data, err = os.ReadFile(jpgFile)
		}

		if err == nil && data != nil {
			return &BlobData{
				Name:   filepath.Base(msg.FilePath),
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

func downloadVoice(as *AppService, msg *WechatMessage) *BlobData {
	doc, err := xmlquery.Parse(strings.NewReader(msg.Message))
	if err != nil {
		return nil
	}

	node := xmlquery.FindOne(doc, "/msg/voicemsg/@clientmsgid")
	if node == nil || len(node.InnerText()) == 0 {
		return nil
	}
	path := node.InnerText()

	ctx, cancel := context.WithTimeout(context.Background(), MediaDownloadTiemout)
	defer cancel()

	voiceFile := filepath.Join(as.Workdir, msg.Self, path+".amr")
	for {
		if PathExists(voiceFile) {
			data, err := os.ReadFile(voiceFile)
			if err == nil && data != nil {
				return &BlobData{
					Name:   filepath.Base(voiceFile),
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

func downloadVideo(as *AppService, msg *WechatMessage) *BlobData {
	ctx, cancel := context.WithTimeout(context.Background(), MediaDownloadTiemout)
	defer cancel()

	var videoFile string
	if len(msg.FilePath) > 0 {
		videoFile = filepath.Join(as.Docdir, msg.FilePath)
	} else {
		videoFile = filepath.Join(as.Docdir, msg.Thumbnail)
		videoFile = strings.TrimSuffix(videoFile, filepath.Ext(videoFile))
		videoFile += ".mp4"
	}
	for {
		if PathExists(videoFile) {
			data, err := os.ReadFile(videoFile)
			if err == nil && data != nil {
				return &BlobData{
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

func downloadSticker(as *AppService, msg *WechatMessage) *BlobData {
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
		return &BlobData{
			Name:   hash,
			Binary: data,
		}
	} else {
		return nil
	}
}

func parseLocation(as *AppService, msg *WechatMessage) *LocationData {
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

	return &LocationData{
		Name:      name,
		Address:   label,
		Latitude:  latitude,
		Longitude: longitude,
	}
}

func getAppType(as *AppService, msg *WechatMessage) int {
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

func parseReply(as *AppService, msg *WechatMessage) (string, *ReplyInfo) {
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

	return titleNode.InnerText(), &ReplyInfo{ID: msgId, Sender: userNode.InnerText()}
}

func parseNotice(as *AppService, msg *WechatMessage) string {
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

func parseApp(as *AppService, msg *WechatMessage) *LinkData {
	doc, err := xmlquery.Parse(strings.NewReader(msg.Message))
	if err != nil {
		return nil
	}

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

	return &LinkData{
		Title:       titleNode.InnerText(),
		Description: des,
		URL:         url,
	}
}

func parseRevoke(as *AppService, msg *WechatMessage) string {
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

func parsePrivateVoIP(as *AppService, msg *WechatMessage) string {
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

func parseSystemMessage(as *AppService, msg *WechatMessage) string {
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
	replaceNode := xmlquery.FindOne(doc, "/sysmsg/revokemsg/replacemsg")
	if replaceNode != nil {
		return replaceNode.InnerText()
	}

	return ""
}

func downloadFile(as *AppService, msg *WechatMessage) *BlobData {
	ctx, cancel := context.WithTimeout(context.Background(), MediaDownloadTiemout)
	defer cancel()

	file := filepath.Join(as.Docdir, msg.FilePath)
	for {
		if PathExists(file) {
			data, err := os.ReadFile(file)
			if err == nil && data != nil {
				return &BlobData{
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

func saveBlob(as *AppService, msg *MatrixMessage) string {
	var data BlobData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		return ""
	}

	fileName := fmt.Sprintf("%x%s", md5.Sum(data.Binary), filepath.Ext(data.Name))
	path := filepath.Join(as.Workdir, fileName)

	if err := os.WriteFile(path, data.Binary, 0o644); err != nil {
		return ""
	}

	return path
}

func PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || errors.Is(err, os.ErrExist)
}

func GetDocDir() string {
	u, _ := user.Current()
	baseDir := filepath.Join(u.HomeDir, "Documents")

	// Old windows path
	if !PathExists(baseDir) {
		baseDir = filepath.Join(u.HomeDir, "My Documents")
	}

	return baseDir
}

func GetWechatDocdir() string {
	baseDir := GetDocDir()

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

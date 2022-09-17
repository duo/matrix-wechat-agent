package internal

import (
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/base64"
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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	pref "google.golang.org/protobuf/reflect/protoreflect"
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

	data, err := base64.StdEncoding.DecodeString(msg.ExtraInfo)
	if err != nil {
		return nil
	}

	fd := makeFileDescriptor()

	extraDesc := fd.Messages().ByName("Extra")
	kvDesc := fd.Messages().ByName("KV")

	pbMsg := dynamicpb.NewMessage(extraDesc)
	if err := proto.Unmarshal(data, pbMsg); err != nil {
		return nil
	}

	var mentionsXML string
	lst := pbMsg.Get(extraDesc.Fields().ByName("kv_list")).List()
	length := lst.Len()
	for i := 0; i < length; i++ {
		ele := lst.Get(i)
		key := ele.Message().Get(kvDesc.Fields().ByName("key")).Int()
		value := ele.Message().Get(kvDesc.Fields().ByName("value")).String()
		if key == 7 {
			mentionsXML = value
		}
	}

	if len(mentionsXML) == 0 {
		return nil
	}

	doc, err := xmlquery.Parse(strings.NewReader(mentionsXML))
	if err != nil {
		return nil
	}

	atuserNode := xmlquery.FindOne(doc, "/msgsource/atuserlist")
	if atuserNode == nil || len(atuserNode.InnerText()) == 0 {
		return nil
	}

	return strings.Split(atuserNode.InnerText(), ",")
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

	videoFile := filepath.Join(as.Docdir, msg.FilePath)
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

	urlNode := xmlquery.FindOne(doc, "/msg/emoji/@cdnurl")
	if urlNode == nil || len(urlNode.InnerText()) == 0 {
		return nil
	}
	url := urlNode.InnerText()
	md5Node := xmlquery.FindOne(doc, "/msg/emoji/@md5")
	if md5Node == nil || len(md5Node.InnerText()) == 0 {
		return nil
	}
	md5 := md5Node.InnerText()

	data, err := GetBytes(url)
	if err == nil {
		return &BlobData{
			Name:   md5,
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
		return "", nil
	}

	msgId, err := strconv.ParseUint(svridNode.InnerText(), 10, 64)
	if err != nil {
		return "", nil
	}

	return titleNode.InnerText(), &ReplyInfo{ID: msgId, Sender: userNode.InnerText()}
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
	path := filepath.Join(as.Tempdir, fileName)

	if err := os.WriteFile(path, data.Binary, 0o644); err != nil {
		return ""
	}

	return path
}

func fetchQRCode(path string) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), MediaDownloadTiemout)
	defer cancel()

	for {
		if PathExists(path) {
			data, err := os.ReadFile(path)
			if err == nil && data != nil {
				return data
			}
		}

		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return nil
		}
	}
}

func PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || errors.Is(err, os.ErrExist)
}

func GetWechatDocdir() string {
	u, _ := user.Current()
	baseDir := filepath.Join(u.HomeDir, "Documents")

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

func makeFileDescriptor() pref.FileDescriptor {
	pb := &descriptorpb.FileDescriptorProto{
		Syntax:  proto.String("proto3"),
		Name:    proto.String("mm.proto"),
		Package: proto.String("mm"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("KV"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:     proto.String("key"),
						JsonName: proto.String("key"),
						Number:   proto.Int32(1),
						Type:     descriptorpb.FieldDescriptorProto_Type(pref.Int32Kind).Enum(),
					},
					{
						Name:     proto.String("value"),
						JsonName: proto.String("value"),
						Number:   proto.Int32(2),
						Type:     descriptorpb.FieldDescriptorProto_Type(pref.StringKind).Enum(),
					},
				},
			},
			{
				Name: proto.String("Extra"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:     proto.String("kv_list"),
						JsonName: proto.String("kv_list"),
						Number:   proto.Int32(3),
						Label:    descriptorpb.FieldDescriptorProto_Label(pref.Repeated).Enum(),
						Type:     descriptorpb.FieldDescriptorProto_Type(pref.MessageKind).Enum(),
						TypeName: proto.String(".mm.KV"),
					},
				},
			},
		},
	}

	fd, err := protodesc.NewFile(pb, nil)
	if err != nil {
		panic(err)
	}

	return fd
}

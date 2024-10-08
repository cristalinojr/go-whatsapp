package whatsapp

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/cristalinojr/go-whatsapp/binary"
	"go.mau.fi/whatsmeow/binary/proto"
)

type MediaType string

const (
	MediaImage    MediaType = "WhatsApp Image Keys"
	MediaVideo    MediaType = "WhatsApp Video Keys"
	MediaAudio    MediaType = "WhatsApp Audio Keys"
	MediaDocument MediaType = "WhatsApp Document Keys"
)

func (wac *Conn) SendRaw(msg *proto.WebMessageInfo, output chan<- error) {
	ch, err := wac.sendProto(msg)
	if err != nil {
		output <- fmt.Errorf("could not send proto: %w", err)
		return
	}
	response := <-ch
	resp := StatusResponse{RequestType: "message sending"}
	if err = json.Unmarshal([]byte(response), &resp); err != nil {
		output <- fmt.Errorf("error decoding sending response: %w", err)
	} else if resp.Status != 200 {
		output <- resp
	} else {
		output <- nil
	}
}

func (wac *Conn) Send(msg interface{}) (string, error) {
	var msgProto *proto.WebMessageInfo

	switch m := msg.(type) {
	case *proto.WebMessageInfo:
		msgProto = m
	case TextMessage:
		msgProto = getTextProto(m)
	case ImageMessage:
		var err error
		m.url, m.mediaKey, m.fileEncSha256, m.fileSha256, m.fileLength, err = wac.Upload(m.Content, MediaImage)
		if err != nil {
			return "ERROR", fmt.Errorf("image upload failed: %v", err)
		}
		msgProto = getImageProto(m)
	case VideoMessage:
		var err error
		m.url, m.mediaKey, m.fileEncSha256, m.fileSha256, m.fileLength, err = wac.Upload(m.Content, MediaVideo)
		if err != nil {
			return "ERROR", fmt.Errorf("video upload failed: %v", err)
		}
		msgProto = getVideoProto(m)
	case DocumentMessage:
		var err error
		m.url, m.mediaKey, m.fileEncSha256, m.fileSha256, m.fileLength, err = wac.Upload(m.Content, MediaDocument)
		if err != nil {
			return "ERROR", fmt.Errorf("document upload failed: %v", err)
		}
		msgProto = getDocumentProto(m)
	case AudioMessage:
		var err error
		m.url, m.mediaKey, m.fileEncSha256, m.fileSha256, m.fileLength, err = wac.Upload(m.Content, MediaAudio)
		if err != nil {
			return "ERROR", fmt.Errorf("audio upload failed: %v", err)
		}
		msgProto = getAudioProto(m)
	case LocationMessage:
		msgProto = GetLocationProto(m)
	case LiveLocationMessage:
		msgProto = GetLiveLocationProto(m)
	case ContactMessage:
		msgProto = getContactMessageProto(m)
	default:
		return "ERROR", fmt.Errorf("cannot match type %T, use message types declared in the package", msg)
	}
	status := proto.WebMessageInfo_PENDING
	msgProto.Status = &status
	ch, err := wac.sendProto(msgProto)
	if err != nil {
		return "ERROR", fmt.Errorf("could not send proto: %v", err)
	}

	select {
	case response := <-ch:
		resp := StatusResponse{RequestType: "message sending"}
		if err = json.Unmarshal([]byte(response), &resp); err != nil {
			return "ERROR", fmt.Errorf("error decoding sending response: %v\n", err)
		} else if resp.Status != 200 {
			return "ERROR", resp
		}
		return getMessageInfo(msgProto).Id, nil
	case <-time.After(wac.msgTimeout):
		return "ERROR", fmt.Errorf("sending message timed out")
	}
}

func (wac *Conn) sendProto(p *proto.WebMessageInfo) (<-chan string, error) {
	n := binary.Node{
		Description: "action",
		Attributes: map[string]string{
			"type":  "relay",
			"epoch": strconv.Itoa(wac.msgCount),
		},
		Content: []interface{}{p},
	}
	return wac.writeBinary(n, message, ignore, p.Key.GetId())
}

// RevokeMessage revokes a message (marks as "message removed") for everyone
func (wac *Conn) RevokeMessage(remotejid, msgid string, fromme bool) (revokeid string, err error) {
	// create a revocation ID (required)
	rawrevocationID := make([]byte, 10)
	rand.Read(rawrevocationID)
	revocationID := strings.ToUpper(hex.EncodeToString(rawrevocationID))
	//
	ts := uint64(time.Now().Unix())
	status := proto.WebMessageInfo_PENDING
	mtype := proto.ProtocolMessage_REVOKE

	revoker := &proto.WebMessageInfo{
		Key: &proto.MessageKey{
			FromMe:    &fromme,
			ID:        &revocationID,
			RemoteJID: &remotejid,
		},
		MessageTimestamp: &ts,
		Message: &proto.Message{
			ProtocolMessage: &proto.ProtocolMessage{
				Type: &mtype,
				Key: &proto.MessageKey{
					FromMe:    &fromme,
					ID:        &msgid,
					RemoteJID: &remotejid,
				},
			},
		},
		Status: &status,
	}
	if _, err := wac.Send(revoker); err != nil {
		return revocationID, err
	}
	return revocationID, nil
}

// DeleteMessage deletes a single message for the user (removes the msgbox). To
// delete the message for everyone, use RevokeMessage
func (wac *Conn) DeleteMessage(remotejid, msgid string, fromMe bool) error {
	ch, err := wac.deleteChatProto(remotejid, msgid, fromMe)
	if err != nil {
		return fmt.Errorf("could not send proto: %v", err)
	}

	select {
	case response := <-ch:
		resp := StatusResponse{RequestType: "message deletion"}
		if err = json.Unmarshal([]byte(response), &resp); err != nil {
			return fmt.Errorf("error decoding deletion response: %v", err)
		} else if resp.Status != 200 {
			return resp
		}
		return nil
	case <-time.After(wac.msgTimeout):
		return fmt.Errorf("deleting message timed out")
	}
}

func (wac *Conn) deleteChatProto(remotejid, msgid string, fromMe bool) (<-chan string, error) {
	tag := fmt.Sprintf("%s.--%d", wac.timeTag, wac.msgCount)

	owner := "true"
	if !fromMe {
		owner = "false"
	}
	n := binary.Node{
		Description: "action",
		Attributes: map[string]string{
			"epoch": strconv.Itoa(wac.msgCount),
			"type":  "set",
		},
		Content: []interface{}{
			binary.Node{
				Description: "chat",
				Attributes: map[string]string{
					"type":  "clear",
					"jid":   remotejid,
					"media": "true",
				},
				Content: []binary.Node{
					{
						Description: "item",
						Attributes: map[string]string{
							"owner": owner,
							"index": msgid,
						},
					},
				},
			},
		},
	}
	return wac.writeBinary(n, chat, expires|skipOffline, tag)
}

func init() {
	rand.Seed(time.Now().UTC().UnixNano())
}

/*
MessageInfo contains general message information. It is part of every of every message type.
*/
type MessageInfo struct {
	Id        string
	RemoteJid string
	SenderJid string
	FromMe    bool
	Timestamp uint64
	PushName  string
	Status    MessageStatus

	Source *proto.WebMessageInfo
}

type MessageStatus int

const (
	Error       MessageStatus = 0
	Pending                   = 1
	ServerAck                 = 2
	DeliveryAck               = 3
	Read                      = 4
	Played                    = 5
)

func getMessageInfo(msg *proto.WebMessageInfo) MessageInfo {
	return MessageInfo{
		Id:        msg.GetKey().GetId(),
		RemoteJid: msg.GetKey().GetRemoteJid(),
		SenderJid: msg.GetKey().GetParticipant(),
		FromMe:    msg.GetKey().GetFromMe(),
		Timestamp: msg.GetMessageTimestamp(),
		Status:    MessageStatus(msg.GetStatus()),
		PushName:  msg.GetPushName(),
		Source:    msg,
	}
}

func getInfoProto(info *MessageInfo) *proto.WebMessageInfo {
	if info.Id == "" || len(info.Id) < 2 {
		b := make([]byte, 10)
		rand.Read(b)
		info.Id = strings.ToUpper(hex.EncodeToString(b))
	}
	if info.Timestamp == 0 {
		info.Timestamp = uint64(time.Now().Unix())
	}
	info.FromMe = true

	status := proto.WebMessageInfo_Status(info.Status)

	return &proto.WebMessageInfo{
		Key: &proto.MessageKey{
			FromMe:    &info.FromMe,
			RemoteJID: &info.RemoteJid,
			ID:        &info.Id,
		},
		MessageTimestamp: &info.Timestamp,
		Status:           &status,
	}
}

/*
ContextInfo represents contextinfo of every message
*/
type ContextInfo struct {
	QuotedMessageID string //StanzaId
	QuotedMessage   *proto.Message
	Participant     string
	IsForwarded     bool
	MentionedJID    []string
}

func getMessageContext(msg *proto.ContextInfo) ContextInfo {
	return ContextInfo{
		QuotedMessageID: msg.GetStanzaId(), //StanzaId
		QuotedMessage:   msg.GetQuotedMessage(),
		Participant:     msg.GetParticipant(),
		IsForwarded:     msg.GetIsForwarded(),
		MentionedJID:    msg.GetMentionedJid(),
	}
}

func getContextInfoProto(context *ContextInfo) *proto.ContextInfo {
	if len(context.QuotedMessageID) > 0 {
		contextInfo := &proto.ContextInfo{
			StanzaID: &context.QuotedMessageID,
		}

		if &context.QuotedMessage != nil {
			contextInfo.QuotedMessage = context.QuotedMessage
			contextInfo.Participant = &context.Participant
		}

		return contextInfo
	}

	return nil
}

/*
TextMessage represents a text message.
*/
type TextMessage struct {
	Info        MessageInfo
	Text        string
	ContextInfo ContextInfo
}

func getTextMessage(msg *proto.WebMessageInfo) TextMessage {
	text := TextMessage{Info: getMessageInfo(msg)}
	if m := msg.GetMessage().GetExtendedTextMessage(); m != nil {
		text.Text = m.GetText()

		text.ContextInfo = getMessageContext(m.GetContextInfo())
	} else {
		text.Text = msg.GetMessage().GetConversation()

	}

	return text
}

func getTextProto(msg TextMessage) *proto.WebMessageInfo {
	p := getInfoProto(&msg.Info)
	contextInfo := getContextInfoProto(&msg.ContextInfo)

	if contextInfo == nil {
		p.Message = &proto.Message{
			Conversation: &msg.Text,
		}
	} else {
		p.Message = &proto.Message{
			ExtendedTextMessage: &proto.ExtendedTextMessage{
				Text:        &msg.Text,
				ContextInfo: contextInfo,
			},
		}
	}

	return p
}

/*
ImageMessage represents a image message. Unexported fields are needed for media up/downloading and media validation.
Provide a io.Reader as Content for message sending.
*/
type ImageMessage struct {
	Info          MessageInfo
	Caption       string
	Thumbnail     []byte
	Type          string
	Content       io.Reader
	url           string
	mediaKey      []byte
	fileEncSha256 []byte
	fileSha256    []byte
	fileLength    uint64
	ContextInfo   ContextInfo
}

func getImageMessage(msg *proto.WebMessageInfo) ImageMessage {
	image := msg.GetMessage().GetImageMessage()

	imageMessage := ImageMessage{
		Info:          getMessageInfo(msg),
		Caption:       image.GetCaption(),
		Thumbnail:     image.GetJPEGThumbnail(),
		url:           image.GetURL(),
		mediaKey:      image.GetMediaKey(),
		Type:          image.GetMimetype(),
		fileEncSha256: image.GetFileEncSHA256(),
		fileSha256:    image.GetFileSHA256(),
		fileLength:    image.GetFileLength(),
		ContextInfo:   getMessageContext(image.GetContextInfo()),
	}

	return imageMessage
}

func getImageProto(msg ImageMessage) *proto.WebMessageInfo {
	p := getInfoProto(&msg.Info)
	contextInfo := getContextInfoProto(&msg.ContextInfo)

	p.Message = &proto.Message{
		ImageMessage: &proto.ImageMessage{
			Caption:       &msg.Caption,
			JPEGThumbnail: msg.Thumbnail,
			URL:           &msg.url,
			MediaKey:      msg.mediaKey,
			Mimetype:      &msg.Type,
			FileEncSHA256: msg.fileEncSha256,
			FileSHA256:    msg.fileSha256,
			FileLength:    &msg.fileLength,
			ContextInfo:   contextInfo,
		},
	}
	return p
}

/*
Download is the function to retrieve media data. The media gets downloaded, validated and returned.
*/
func (m *ImageMessage) Download() ([]byte, error) {
	return Download(m.url, m.mediaKey, MediaImage, int(m.fileLength))
}

/*
VideoMessage represents a video message. Unexported fields are needed for media up/downloading and media validation.
Provide a io.Reader as Content for message sending.
*/
type VideoMessage struct {
	Info          MessageInfo
	Caption       string
	Thumbnail     []byte
	Length        uint32
	Type          string
	Content       io.Reader
	GifPlayback   bool
	url           string
	mediaKey      []byte
	fileEncSha256 []byte
	fileSha256    []byte
	fileLength    uint64
	ContextInfo   ContextInfo
}

func getVideoMessage(msg *proto.WebMessageInfo) VideoMessage {
	vid := msg.GetMessage().GetVideoMessage()

	videoMessage := VideoMessage{
		Info:          getMessageInfo(msg),
		Caption:       vid.GetCaption(),
		Thumbnail:     vid.GetJPEGThumbnail(),
		GifPlayback:   vid.GetGifPlayback(),
		url:           vid.GetURL(),
		mediaKey:      vid.GetMediaKey(),
		Length:        vid.GetSeconds(),
		Type:          vid.GetMimetype(),
		fileEncSha256: vid.GetFileEncSHA256(),
		fileSha256:    vid.GetFileSHA256(),
		fileLength:    vid.GetFileLength(),
		ContextInfo:   getMessageContext(vid.GetContextInfo()),
	}

	return videoMessage
}

func getVideoProto(msg VideoMessage) *proto.WebMessageInfo {
	p := getInfoProto(&msg.Info)
	contextInfo := getContextInfoProto(&msg.ContextInfo)

	p.Message = &proto.Message{
		VideoMessage: &proto.VideoMessage{
			Caption:       &msg.Caption,
			JPEGThumbnail: msg.Thumbnail,
			URL:           &msg.url,
			GifPlayback:   &msg.GifPlayback,
			MediaKey:      msg.mediaKey,
			Seconds:       &msg.Length,
			FileEncSHA256: msg.fileEncSha256,
			FileSHA256:    msg.fileSha256,
			FileLength:    &msg.fileLength,
			Mimetype:      &msg.Type,
			ContextInfo:   contextInfo,
		},
	}
	return p
}

/*
Download is the function to retrieve media data. The media gets downloaded, validated and returned.
*/
func (m *VideoMessage) Download() ([]byte, error) {
	return Download(m.url, m.mediaKey, MediaVideo, int(m.fileLength))
}

/*
AudioMessage represents a audio message. Unexported fields are needed for media up/downloading and media validation.
Provide a io.Reader as Content for message sending.
*/
type AudioMessage struct {
	Info          MessageInfo
	Length        uint32
	Type          string
	Content       io.Reader
	Ptt           bool
	url           string
	mediaKey      []byte
	fileEncSha256 []byte
	fileSha256    []byte
	fileLength    uint64
	ContextInfo   ContextInfo
}

func getAudioMessage(msg *proto.WebMessageInfo) AudioMessage {
	aud := msg.GetMessage().GetAudioMessage()

	audioMessage := AudioMessage{
		Info:          getMessageInfo(msg),
		url:           aud.GetURL(),
		mediaKey:      aud.GetMediaKey(),
		Length:        aud.GetSeconds(),
		Type:          aud.GetMimetype(),
		fileEncSha256: aud.GetFileEncSHA256(),
		fileSha256:    aud.GetFileSHA256(),
		fileLength:    aud.GetFileLength(),
		ContextInfo:   getMessageContext(aud.GetContextInfo()),
	}

	return audioMessage
}

func getAudioProto(msg AudioMessage) *proto.WebMessageInfo {
	p := getInfoProto(&msg.Info)
	contextInfo := getContextInfoProto(&msg.ContextInfo)
	p.Message = &proto.Message{
		AudioMessage: &proto.AudioMessage{
			URL:           &msg.url,
			MediaKey:      msg.mediaKey,
			Seconds:       &msg.Length,
			FileEncSHA256: msg.fileEncSha256,
			FileSHA256:    msg.fileSha256,
			FileLength:    &msg.fileLength,
			Mimetype:      &msg.Type,
			ContextInfo:   contextInfo,
			PTT:           &msg.Ptt,
		},
	}
	return p
}

/*
Download is the function to retrieve media data. The media gets downloaded, validated and returned.
*/
func (m *AudioMessage) Download() ([]byte, error) {
	return Download(m.url, m.mediaKey, MediaAudio, int(m.fileLength))
}

/*
DocumentMessage represents a document message. Unexported fields are needed for media up/downloading and media
validation. Provide a io.Reader as Content for message sending.
*/
type DocumentMessage struct {
	Info          MessageInfo
	Title         string
	PageCount     uint32
	Type          string
	FileName      string
	Thumbnail     []byte
	Content       io.Reader
	url           string
	mediaKey      []byte
	fileEncSha256 []byte
	fileSha256    []byte
	fileLength    uint64
	ContextInfo   ContextInfo
}

func getDocumentMessage(msg *proto.WebMessageInfo) DocumentMessage {
	doc := msg.GetMessage().GetDocumentMessage()

	documentMessage := DocumentMessage{
		Info:          getMessageInfo(msg),
		Title:         doc.GetTitle(),
		PageCount:     doc.GetPageCount(),
		Type:          doc.GetMimetype(),
		FileName:      doc.GetFileName(),
		Thumbnail:     doc.GetJPEGThumbnail(),
		url:           doc.GetURL(),
		mediaKey:      doc.GetMediaKey(),
		fileEncSha256: doc.GetFileEncSHA256(),
		fileSha256:    doc.GetFileSHA256(),
		fileLength:    doc.GetFileLength(),
		ContextInfo:   getMessageContext(doc.GetContextInfo()),
	}

	return documentMessage
}

func getDocumentProto(msg DocumentMessage) *proto.WebMessageInfo {
	p := getInfoProto(&msg.Info)
	contextInfo := getContextInfoProto(&msg.ContextInfo)
	p.Message = &proto.Message{
		DocumentMessage: &proto.DocumentMessage{
			JPEGThumbnail: msg.Thumbnail,
			URL:           &msg.url,
			MediaKey:      msg.mediaKey,
			FileEncSHA256: msg.fileEncSha256,
			FileSHA256:    msg.fileSha256,
			FileLength:    &msg.fileLength,
			PageCount:     &msg.PageCount,
			Title:         &msg.Title,
			Mimetype:      &msg.Type,
			ContextInfo:   contextInfo,
		},
	}
	return p
}

/*
Download is the function to retrieve media data. The media gets downloaded, validated and returned.
*/
func (m *DocumentMessage) Download() ([]byte, error) {
	return Download(m.url, m.mediaKey, MediaDocument, int(m.fileLength))
}

/*
LocationMessage represents a location message
*/
type LocationMessage struct {
	Info             MessageInfo
	DegreesLatitude  float64
	DegreesLongitude float64
	Name             string
	Address          string
	Url              string
	JpegThumbnail    []byte
	ContextInfo      ContextInfo
}

func GetLocationMessage(msg *proto.WebMessageInfo) LocationMessage {
	loc := msg.GetMessage().GetLocationMessage()

	locationMessage := LocationMessage{
		Info:             getMessageInfo(msg),
		DegreesLatitude:  loc.GetDegreesLatitude(),
		DegreesLongitude: loc.GetDegreesLongitude(),
		Name:             loc.GetName(),
		Address:          loc.GetAddress(),
		Url:              loc.GetURL(),
		JpegThumbnail:    loc.GetJPEGThumbnail(),
		ContextInfo:      getMessageContext(loc.GetContextInfo()),
	}

	return locationMessage
}

func GetLocationProto(msg LocationMessage) *proto.WebMessageInfo {
	p := getInfoProto(&msg.Info)
	contextInfo := getContextInfoProto(&msg.ContextInfo)

	p.Message = &proto.Message{
		LocationMessage: &proto.LocationMessage{
			DegreesLatitude:  &msg.DegreesLatitude,
			DegreesLongitude: &msg.DegreesLongitude,
			Name:             &msg.Name,
			Address:          &msg.Address,
			URL:              &msg.Url,
			JPEGThumbnail:    msg.JpegThumbnail,
			ContextInfo:      contextInfo,
		},
	}
	return p
}

/*
LiveLocationMessage represents a live location message
*/
type LiveLocationMessage struct {
	Info                              MessageInfo
	DegreesLatitude                   float64
	DegreesLongitude                  float64
	AccuracyInMeters                  uint32
	SpeedInMps                        float32
	DegreesClockwiseFromMagneticNorth uint32
	Caption                           string
	SequenceNumber                    int64
	JpegThumbnail                     []byte
	ContextInfo                       ContextInfo
}

func GetLiveLocationMessage(msg *proto.WebMessageInfo) LiveLocationMessage {
	loc := msg.GetMessage().GetLiveLocationMessage()

	liveLocationMessage := LiveLocationMessage{
		Info:                              getMessageInfo(msg),
		DegreesLatitude:                   loc.GetDegreesLatitude(),
		DegreesLongitude:                  loc.GetDegreesLongitude(),
		AccuracyInMeters:                  loc.GetAccuracyInMeters(),
		SpeedInMps:                        loc.GetSpeedInMps(),
		DegreesClockwiseFromMagneticNorth: loc.GetDegreesClockwiseFromMagneticNorth(),
		Caption:                           loc.GetCaption(),
		SequenceNumber:                    loc.GetSequenceNumber(),
		JpegThumbnail:                     loc.GetJPEGThumbnail(),
		ContextInfo:                       getMessageContext(loc.GetContextInfo()),
	}

	return liveLocationMessage
}

func GetLiveLocationProto(msg LiveLocationMessage) *proto.WebMessageInfo {
	p := getInfoProto(&msg.Info)
	contextInfo := getContextInfoProto(&msg.ContextInfo)
	p.Message = &proto.Message{
		LiveLocationMessage: &proto.LiveLocationMessage{
			DegreesLatitude:                   &msg.DegreesLatitude,
			DegreesLongitude:                  &msg.DegreesLongitude,
			AccuracyInMeters:                  &msg.AccuracyInMeters,
			SpeedInMps:                        &msg.SpeedInMps,
			DegreesClockwiseFromMagneticNorth: &msg.DegreesClockwiseFromMagneticNorth,
			Caption:                           &msg.Caption,
			SequenceNumber:                    &msg.SequenceNumber,
			JPEGThumbnail:                     msg.JpegThumbnail,
			ContextInfo:                       contextInfo,
		},
	}
	return p
}

/*
StickerMessage represents a sticker message.
*/
type StickerMessage struct {
	Info MessageInfo

	Type          string
	Content       io.Reader
	url           string
	mediaKey      []byte
	fileEncSha256 []byte
	fileSha256    []byte
	fileLength    uint64

	ContextInfo ContextInfo
}

func getStickerMessage(msg *proto.WebMessageInfo) StickerMessage {
	sticker := msg.GetMessage().GetStickerMessage()

	stickerMessage := StickerMessage{
		Info:          getMessageInfo(msg),
		url:           sticker.GetURL(),
		mediaKey:      sticker.GetMediaKey(),
		Type:          sticker.GetMimetype(),
		fileEncSha256: sticker.GetFileEncSHA256(),
		fileSha256:    sticker.GetFileSHA256(),
		fileLength:    sticker.GetFileLength(),
		ContextInfo:   getMessageContext(sticker.GetContextInfo()),
	}

	return stickerMessage
}

/*
Download is the function to retrieve Sticker media data. The media gets downloaded, validated and returned.
*/

func (m *StickerMessage) Download() ([]byte, error) {
	return Download(m.url, m.mediaKey, MediaImage, int(m.fileLength))
}

/*
ContactMessage represents a contact message.
*/
type ContactMessage struct {
	Info MessageInfo

	DisplayName string
	Vcard       string

	ContextInfo ContextInfo
}

func getContactMessage(msg *proto.WebMessageInfo) ContactMessage {
	contact := msg.GetMessage().GetContactMessage()

	contactMessage := ContactMessage{
		Info: getMessageInfo(msg),

		DisplayName: contact.GetDisplayName(),
		Vcard:       contact.GetVcard(),

		ContextInfo: getMessageContext(contact.GetContextInfo()),
	}

	return contactMessage
}

func getContactMessageProto(msg ContactMessage) *proto.WebMessageInfo {
	p := getInfoProto(&msg.Info)
	contextInfo := getContextInfoProto(&msg.ContextInfo)

	p.Message = &proto.Message{
		ContactMessage: &proto.ContactMessage{
			DisplayName: &msg.DisplayName,
			Vcard:       &msg.Vcard,
			ContextInfo: contextInfo,
		},
	}

	return p
}

func ParseProtoMessage(msg *proto.WebMessageInfo) interface{} {

	switch {

	case msg.GetMessage().GetAudioMessage() != nil:
		return getAudioMessage(msg)

	case msg.GetMessage().GetImageMessage() != nil:
		return getImageMessage(msg)

	case msg.GetMessage().GetVideoMessage() != nil:
		return getVideoMessage(msg)

	case msg.GetMessage().GetDocumentMessage() != nil:
		return getDocumentMessage(msg)

	case msg.GetMessage().GetConversation() != "":
		return getTextMessage(msg)

	case msg.GetMessage().GetExtendedTextMessage() != nil:
		return getTextMessage(msg)

	case msg.GetMessage().GetLocationMessage() != nil:
		return GetLocationMessage(msg)

	case msg.GetMessage().GetLiveLocationMessage() != nil:
		return GetLiveLocationMessage(msg)

	case msg.GetMessage().GetStickerMessage() != nil:
		return getStickerMessage(msg)

	case msg.GetMessage().GetContactMessage() != nil:
		return getContactMessage(msg)

	default:
		//cannot match message

	}

	return nil
}

/*
BatteryMessage represents a battery level and charging state.
*/
type BatteryMessage struct {
	Plugged    bool
	Powersave  bool
	Percentage int
}

func getBatteryMessage(msg map[string]string) BatteryMessage {
	plugged, _ := strconv.ParseBool(msg["live"])
	powersave, _ := strconv.ParseBool(msg["powersave"])
	percentage, _ := strconv.Atoi(msg["value"])
	batteryMessage := BatteryMessage{
		Plugged:    plugged,
		Powersave:  powersave,
		Percentage: percentage,
	}

	return batteryMessage
}

func getNewContact(msg map[string]string) Contact {
	contact := Contact{
		Jid:    msg["jid"],
		Notify: msg["notify"],
	}

	return contact
}

// ReadMessage represents a chat that the user read on the WhatsApp mobile app.
type ReadMessage struct {
	Jid string
}

func getReadMessage(msg map[string]string) ReadMessage {
	return ReadMessage{
		Jid: msg["jid"],
	}
}

// ReceivedMessage probably represents a message that the user read on the WhatsApp mobile app.
type ReceivedMessage struct {
	Index       string
	Jid         string
	Owner       bool
	Participant string
	Type        string
}

func getReceivedMessage(msg map[string]string) ReceivedMessage {
	owner, _ := strconv.ParseBool(msg["owner"])
	// This field might not exist
	participant, _ := msg["participant"]
	return ReceivedMessage{
		Index:       msg["index"],
		Jid:         msg["jid"],
		Owner:       owner,
		Participant: participant,
		Type:        msg["type"],
	}
}

func ParseNodeMessage(msg binary.Node) interface{} {
	switch msg.Description {
	case "battery":
		return getBatteryMessage(msg.Attributes)
	case "user":
		return getNewContact(msg.Attributes)
	case "read":
		return getReadMessage(msg.Attributes)
	case "received":
		return getReceivedMessage(msg.Attributes)
	default:
		return &msg
	}
}

/*
 * WebRTC Demo
 *
 * This module demonstrates the WebRTC API by implementing a simple video chat app.
 *
 * Rewritten in Golang by Daniel G. Vargas
 * Based on https://github.com/webrtc/apprtc/blob/master/src/app_engine/apprtc.py (rev.69c3024)
 * Look browser support on http://iswebrtcreadyyet.com/
 *
 */
package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"goapprtc/collider"
	"golang.org/x/net/websocket"
)

const (
	// Deprecated domains which we should to redirect to REDIRECT_URL.
	//REDIRECT_DOMAINS = "goapprtc.fly.dev" //"goapprtc.appspot.com"
	// URL which we should redirect to if matching in REDIRECT_DOMAINS.
	//REDIRECT_URL = "https://goapprtc.fly.dev"

	LOOPBACK_CLIENT_ID = "LOOPBACK_CLIENT_ID"

	/* Turn/Stun server override. This allows AppRTC to connect to turn servers
	   # directly rather than retrieving them from an ICE server provider.
	   ICE_SERVER_OVERRIDE = None
	   # Enable by uncomment below and comment out above, then specify turn and stun
	   # ICE_SERVER_OVERRIDE  = [
	   #   {
	   #     "urls": [
	   #       "turn:hostname/IpToTurnServer:19305?transport=udp",
	   #       "turn:hostname/IpToTurnServer:19305?transport=tcp"
	   #     ],
	   #     "username": "TurnServerUsername",
	   #     "credential": "TurnServerCredentials"
	   #   },
	   #   {
	   #     "urls": [
	   #       "stun:hostname/IpToStunServer:19302"
	   #     ]
	   #   }
	   # ]
	*/

	ICE_SERVER_BASE_URL     = ""
	ICE_SERVER_URL_TEMPLATE = "%s/v1alpha/iceconfig?key=%s"
	ICE_SERVER_URLS         = "turn:192.99.9.67:3478?transport=udp"

	// Dictionary keys in the collider probing result.
	WSS_HOST_IS_UP_KEY         = "is_up"
	WSS_HOST_STATUS_CODE_KEY   = "status_code"
	WSS_HOST_ERROR_MESSAGE_KEY = "error_message"

	RESPONSE_ERROR            = "ERROR"
	RESPONSE_ROOM_FULL        = "FULL"
	RESPONSE_UNKNOWN_ROOM     = "UNKNOWN_ROOM"
	RESPONSE_UNKNOWN_CLIENT   = "UNKNOWN_CLIENT"
	RESPONSE_DUPLICATE_CLIENT = "DUPLICATE_CLIENT"
	RESPONSE_SUCCESS          = "SUCCESS"
	RESPONSE_INVALID_REQUEST  = "INVALID_REQUEST"
)

var (
	HEADER_MESSAGE      = os.Getenv("HEADER_MESSAGE")
	ICE_SERVER_API_KEY  = os.Getenv("ICE_SERVER_API_KEY")
	ICE_SERVER_OVERRIDE = os.Getenv("ICE_SERVERS")
	WSS_HOST            = os.Getenv("WSS_HOST")
)

type Config struct {
	IceServers    []IceServers `json:"iceServers"`
	IceTransports []string     `json:"iceTransports,omitempty"`
	BundlePolicy  string       `json:"bundlePolicy,omitempty"`
	RtcpMuxPolicy string       `json:"rtcpMuxPolicy,omitempty"`
}

type Options struct {
	DtlsSrtpKeyAgreement bool `json:"DtlsSrtpKeyAgreement,omitempty"`
	googDscp             bool `json:"googDscp,omitempty"`
	googIPv6             bool `json:"googIPv6,omitempty"`
}

type Constraints struct {
	Optional  []Options   `json:"optional"`
	Mandatory interface{} `json:"mandatory,omitempty"`
}

type MediaConstraint struct {
	Audio bool `json:"audio"`
	Video bool `json:"video"`
	Fake  bool `json:"fake"`
}

type IceServers struct {
	Urls       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type SDP struct {
	Type      string `json:"type"`
	Sdp       string `json:"sdp,omitempty"`
	Id        string `json:"id,omitempty"`
	Candidate string `json:"candidate,omitempty"`
	Label     int    `json:"label,omitempty"`
}

type Params struct {
	WssPostURL             string          `json:"wss_post_url,omitempty"`
	MediaConstraints       MediaConstraint `json:"media_constraints"`
	IsLoopback             string          `json:"is_loopback"`
	HeaderMessage          string          `json:"header_message"`
	IceServerURL           string          `json:"ice_server_url,omitempty"`
	ErrorMessages          []string        `json:"error_messages"`
	IceServerTransports    string          `json:"ice_server_transports"`
	PcConfig               Config          `json:"pc_config,omitempty"`
	WarningMessages        []string        `json:"warning_messages"`
	PcConstraints          Constraints     `json:"pc_constraints,omitempty"`
	WssURL                 string          `json:"wss_url,omitempty"`
	OfferOptions           string          `json:"offer_options,omitempty"`
	VersionInfo            string          `json:"version_info,omitempty"`
	BypassJoinConfirmation bool            `json:"bypass_join_confirmation"`
	IncludeLoopbackJs      string          `json:"include_loopback_js"`
	RoomID                 string          `json:"room_id"`
	ClientID               string          `json:"client_id,omitempty"`
	RoomLink               string          `json:"room_link"`
	IsInitiator            string          `json:"is_initiator,omitempty"`
	Messages               []string        `json:"messages,omitempty"`
}

type Client struct {
	isInitiator bool
	messages    []string
}

var rooms = make(map[string]Room)

// All the data we store for a room
type Room struct {
	clients    map[string]*Client
	isLoopback bool
}

func generateRandom(length int) string {
	word := ""
	for i := 0; i < length; i++ {
		rand.Seed(time.Now().UTC().UnixNano())
		word += strconv.Itoa(rand.Intn(10))
	}
	return word
}

func getHdDefault(userAgent string) bool {
	if strings.Contains(userAgent, "Android") || !strings.Contains(userAgent, "Chrome") {
		return false
	}
	return true
}

func sanitize(key string) string {
	re := regexp.MustCompile("[^a-zA-Z0-9]")
	return re.ReplaceAllString(key, "-")
}

func makeClientId(room, user string) string {
	return room + "/" + user
}

func getPreferredAudioSendCodec(user_agent string) string {
	var preferred_audio_send_codec = ""
	if (strings.Contains(user_agent, "Android")) && (strings.Contains(user_agent, "Chrome")) {
		preferred_audio_send_codec = "ISAC/16000"
	}
	return preferred_audio_send_codec
}

func makePcConfig(iceTransports string, iceServerOverride string) Config {
	is := []IceServers{}
	if iceServerOverride != "" {
		err := json.Unmarshal([]byte(iceServerOverride), &is)
		if err != nil {
			log.Printf("makePcConfig: error while trying to Unmarshal ice_server json %v", err)
			return Config{}
		}
	}
	it := []string{}
	if iceTransports != "" {
		it = strings.Split(iceTransports, ",")
	}
	return Config{IceServers: is,
		IceTransports: it,
		BundlePolicy:  "max-bundle",
		RtcpMuxPolicy: "require"}
}

func makeLoopbackAnswer(message string) string {
	message = strings.Replace(message, `"offer"`, `"answer"`, -1)
	message = strings.Replace(message, "a=ice-options:google-ice\r\n", "", -1)
	return message
}

func makeMediaTrackConstraints(constraints string) (track_constraints bool) {
	optl := make([]map[string]string, 0)
	mand := map[string]string{}
	if constraints == "" || strings.ToLower(constraints) == "true" {
		track_constraints = true
	} else if strings.ToLower(constraints) == "false" {
		track_constraints = false
	} else {
		constraints_ := strings.Split(constraints, ",")
		for i := 0; i < len(constraints_); i++ {
			c := strings.Split(constraints_[i], "=")
			if len(c) == 2 {
				if strings.Contains(c[0], "goog") {
					optl = append(optl, map[string]string{c[0]: c[1]})
				} else {
					mand[c[0]] = c[1]
				}
			}
		}
	}
	return track_constraints
}

func makeMediaStreamConstraints(audio, video, firefoxFakeDevice string) MediaConstraint {
	var isFakeDev bool
	if firefoxFakeDevice != "" {
		isFakeDev = makeMediaTrackConstraints(firefoxFakeDevice)
	}
	return MediaConstraint{Audio: makeMediaTrackConstraints(audio), Video: makeMediaTrackConstraints(video), Fake: isFakeDev}
}

func makeOfferConstraints() Constraints {
	return Constraints{Optional: []Options{}, Mandatory: map[string]string{}}
}

func (c *Client) addMessage(msg string) {
	c.messages = append(c.messages, msg)
}

func (c *Client) clearMessages() {
	c.messages = []string{}
}

func (c *Client) setInitiator() {
	c.isInitiator = true
}

func (r *Room) addClient(clientId string, client *Client) {
	if r.clients == nil {
		r.clients = make(map[string]*Client)
	}
	r.clients[clientId] = client
}

func (r *Room) remove(clientId string) {
	delete(r.clients, clientId)
}

func (r *Room) getOccupancy() int {
	return len(r.clients)
}

func (r *Room) hasClient(clientId string) bool {
	_, ok := r.clients[clientId]
	return ok
}

func (r *Room) getClient(clientId string) (c *Client) {
	c, _ = r.clients[clientId]
	return
}

func (r *Room) getOtherClient(clientId string) *Client {
	for k, _ := range r.clients {
		if k != clientId {
			return r.clients[k]
		}
	}
	return &Client{}
}

func addClientToRoom(roomId, clientId string, isLoopback bool) (error string, isInitiator bool, messages []string, roomState string) {
	r, ok := rooms[roomId]
	if !ok {
		rooms[roomId] = Room{}
	}
	occupancy := r.getOccupancy()
	if occupancy >= 2 {
		error = RESPONSE_ROOM_FULL
		return
	}
	if r.hasClient(clientId) {
		error = RESPONSE_DUPLICATE_CLIENT
		return
	}

	if r.hasClient(LOOPBACK_CLIENT_ID) {
		r.remove(LOOPBACK_CLIENT_ID)
	}

	if occupancy == 0 {
		r.addClient(clientId, &Client{true, []string{}})
		r.clients[clientId].setInitiator()
		isInitiator = true
	} else {
		oc := r.getOtherClient(clientId)
		messages = oc.messages
		oc.clearMessages()
		r.addClient(clientId, &Client{true, []string{}})
	}
	rooms[roomId] = r
	log.Printf("Added client %s in room %s", clientId, roomId)
	return
}

func removeClientFromRoom(roomId, clientId string) (error, roomState string) {
	r, ok := rooms[roomId]
	if !ok {
		log.Println("remove_client_from_room: Unknown room", roomId)
		return RESPONSE_UNKNOWN_ROOM, ""
	}
	if !r.hasClient(clientId) {
		log.Println("remove_client_from_room: Unknown client", clientId)
		return RESPONSE_UNKNOWN_CLIENT, ""
	}
	r.remove(clientId)
	if r.hasClient(LOOPBACK_CLIENT_ID) {
		r.remove(LOOPBACK_CLIENT_ID)
	}
	if r.getOccupancy() > 0 {
		oc := r.getOtherClient(clientId)
		oc.setInitiator()
	}
	log.Printf("Removed client %s from room %s", clientId, roomId)
	return
}

func saveMessageFromClient(host, roomId, clientId, message string) (error string, saved bool) {
	r, ok := rooms[roomId]
	if !ok {
		error = RESPONSE_UNKNOWN_ROOM
		saved = false
		return
	}
	c, ok := r.clients[clientId]
	if !ok {
		error = RESPONSE_UNKNOWN_CLIENT
		saved = false
		return
	}
	c.addMessage(message)
	log.Printf("Saved message for client %s in room %s", clientId, roomId)
	return
}

func sendMessageToCollider(r *http.Request, roomId, clientId, message string) {
	log.Printf("Forwarding message to collider for room %s client %s", roomId, clientId)
	_, wssPostUrl := getWssParameters(r)
	u := wssPostUrl + "/" + roomId + "/" + clientId
	resp, err := http.PostForm(u, url.Values{"room_id": {roomId}, "client_id": {clientId}, "message": {message}})
	if err != nil {
		log.Printf("Failed to PostForm: %v", err)
	}
	if resp.StatusCode != 200 {
		log.Printf("Failed to send message to collider: %d", resp.StatusCode)
	}
}

type result struct {
	Result  string `json:"result"`
	Params_ Params `json:"params"`
}

func messagePage(w http.ResponseWriter, r *http.Request) {
	roomId := mux.Vars(r)["roomid"]
	clientId := mux.Vars(r)["clientid"]
	if roomId != "" && clientId != "" {
		defer r.Body.Close()
		messageJson, _ := ioutil.ReadAll(r.Body)
		error, saved := saveMessageFromClient(r.RequestURI, roomId, clientId, string(messageJson))
		if error != "" {
			return
		}

		rlt := map[string]string{"result": ""}
		if !saved {
			sendMessageToCollider(r, roomId, clientId, string(messageJson))
		}
		if error == "" {
			rlt["result"] = RESPONSE_SUCCESS
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(rlt)
	}
}

func makePcConstraints(dtls, dscp, ipv6 string) Constraints {
	opts := Options{}
	if strings.ToLower(dtls) == "true" {
		opts.DtlsSrtpKeyAgreement = true
	}
	if strings.ToLower(dscp) == "true" {
		opts.googDscp = true
	}
	if strings.ToLower(ipv6) == "true" {
		opts.googIPv6 = true
	}
	return Constraints{Optional: []Options{opts}}
}

func maybeUseHttpsHostUrl(r *http.Request) string {
	if r.URL.Query().Get("wstls") == "true" || !strings.Contains(r.Host, "localhost") {
		// Assume AppRTC is running behind a stunnel proxy and fix base URL.
		return "https://" + r.Host
	}
	return "http://" + r.Host
}

func getWssParameters(r *http.Request) (wssUrl, wssPostUrl string) {
	q := r.URL.Query()
	wssHostPortPair := q.Get("wshpp")
	wssTls := q.Get("wstls")
	if wssHostPortPair == "" {
		if WSS_HOST != "" {
			wssHostPortPair = WSS_HOST
		} else {
			wssHostPortPair = r.Host
		}
	}
	if wssTls == "false" || strings.Contains(wssHostPortPair, "localhost") {
		wssUrl = "ws://" + wssHostPortPair + "/ws"
		wssPostUrl = "http://" + wssHostPortPair
	} else {
		wssUrl = "wss://" + wssHostPortPair + "/ws"
		wssPostUrl = "https://" + wssHostPortPair
	}
	return
}

// Returns appropriate room parameters based on query parameters in the request.
// TODO(tkchin): move query parameter parsing to JS code.
func getRoomParameters(r *http.Request, roomId, clientId string, isInitiator bool) (params Params, err error) {
	q := r.URL.Query()
	// Append strings to this list to have them thrown up in message boxes. This
	// will also cause the app to fail.
	errorMessages := []string{}
	warningMessages := []string{}
	var iceServerUrl, message string
	userAgent := r.UserAgent()

	//  HTML or JSON.
	//responseType := q.Get("t")
	//  Which ICE candidates to allow. This is useful for forcing a call to run
	//  over TURN, by setting it=relay.
	iceTransports := q.Get("it")
	//  Which ICE server transport= to allow (i.e., only TURN URLs with
	//  transport=<tt> will be used). This is useful for forcing a session to use
	//  TURN/TCP, by setting it=relay&tt=tcp.
	iceServerTransports := q.Get("tt")
	//  A HTTP server that will be used to find the right ICE servers to use, as
	//  described in http://tools.ietf.org/html/draft-uberti-rtcweb-turn-rest-00.
	iceServerBaseUrl := ICE_SERVER_BASE_URL
	if iceServerBaseUrl == "" {
		iceServerBaseUrl = q.Get("ts")
	}

	// 	Use "audio" and "video" to set the media stream constraints. Defined here:
	// 	http://goo.gl/V7cZg
	//
	// 	"true" and "false" are recognized and interpreted as bools, for example:
	// 	  "?audio=true&video=false" (Start an audio-only call.)
	// 	  "?audio=false" (Start a video-only call.)
	// 	If unspecified, the stream constraint defaults to True.
	//
	// 	To specify media track constraints, pass in a comma-separated list of
	// 	key/value pairs, separated by a "=". Examples:
	// 	  "?audio=googEchoCancellation=false,googAutoGainControl=true"
	// 	  (Disable echo cancellation and enable gain control.)
	//
	// 	  "?video=minWidth=1280,minHeight=720,googNoiseReduction=true"
	// 	  (Set the minimum resolution to 1280x720 and enable noise reduction.)
	//
	// 	Keys starting with "goog" will be added to the "optional" key; all others
	// 	will be added to the "mandatory" key.
	// 	To override this default behavior, add a "mandatory" or "optional" prefix
	// 	to each key, e.g.
	// 	  "?video=optional:minWidth=1280,optional:minHeight=720,
	// 			  mandatory:googNoiseReduction=true"
	// 	  (Try to do 1280x720, but be willing to live with less; enable
	// 	   noise reduction or die trying.)
	//
	// 	The audio keys are defined here: talk/app/webrtc/localaudiosource.cc
	// 	The video keys are defined here: talk/app/webrtc/videosource.cc
	audio := q.Get("audio")
	video := q.Get("video")

	//  Pass firefox_fake_device=1 to pass fake: true in the media constraints,
	//  which will make Firefox use its built-in fake device.
	firefoxFakeDevice := q.Get("firefox_fake_device")

	//  The hd parameter is a shorthand to determine whether to open the
	//  camera at 720p. If no value is provided, use a platform-specific default.
	//  When defaulting to HD, use optional constraints, in case the camera
	//  doesn't actually support HD modes.
	hd := strings.ToLower(q.Get("hd"))
	if hd != "" && video != "" {
		message = `The "hd" parameter has overridden video=` + video
		log.Println(message)
		warningMessages = append(warningMessages, message)
		if hd == "true" {
			video = "mandatory:minWidth=1280,mandatory:minHeight=720"
		}
	} else {
		if hd == "" && video == "" && getHdDefault(userAgent) {
			video = "optional:minWidth=1280,optional:minHeight=720"
		}
	}
	if q.Get("minre") != "" || q.Get("maxre") != "" {
		message = `The "minre" and "maxre" parameters are no longer supported. Use "video" instead.`
		log.Println(message)
		warningMessages = append(warningMessages, message)
	}

	//  Options for controlling various networking features.
	dtls := q.Get("dtls")
	dscp := q.Get("dscp")
	ipv6 := q.Get("ipv6")

	debug := q.Get("debug")
	var includeLoopbackJs string
	if debug == "loopback" {
		// Set dtls to false as DTLS does not work for loopback.
		dtls = "false"
		includeLoopbackJs = `<script src="/js/loopback.js"></script>`
	} else {
		includeLoopbackJs = ""
	}

	//  TODO(tkchin): We want to provide a ICE request url on the initial get,
	//  but we don't provide client_id until a join. For now just generate
	//  a random id, but we should make this better.
	if len(iceServerBaseUrl) > 0 {
		apiKey := q.Get("apikey")
		if apiKey == "" {
			apiKey = ICE_SERVER_API_KEY
		}
		iceServerUrl = fmt.Sprintf("%s/v1alpha/iceconfig?key=%s", iceServerBaseUrl, apiKey)
	}
	//  If defined it will override the ICE server provider and use the specified
	//  turn servers directly.
	pcConfig := makePcConfig(iceTransports, ICE_SERVER_OVERRIDE)
	pcConstraints := makePcConstraints(dtls, dscp, ipv6) // Constraints{Optional: []string{}}
	mediaConstraints := makeMediaStreamConstraints(audio, video, firefoxFakeDevice)
	wssUrl, wssPostUrl := getWssParameters(r)
	isLoopback := "false"
	if debug == "loopback" {
		isLoopback = "true"
	}
	params = Params{
		HeaderMessage:          HEADER_MESSAGE,
		ErrorMessages:          errorMessages,
		WarningMessages:        warningMessages,
		IsLoopback:             isLoopback,
		PcConfig:               pcConfig,
		PcConstraints:          pcConstraints,
		OfferOptions:           "{}",
		MediaConstraints:       mediaConstraints,
		IceServerURL:           iceServerUrl,
		IceServerTransports:    iceServerTransports,
		IncludeLoopbackJs:      includeLoopbackJs,
		WssURL:                 wssUrl,
		WssPostURL:             wssPostUrl,
		BypassJoinConfirmation: os.Getenv("BYPASS_JOIN_CONFIRMATION") == "True",
		VersionInfo:            os.Getenv("VERSION_INFO"),
		RoomLink:               "",
	}
	if roomId != "" {
		roomLink := maybeUseHttpsHostUrl(r) + "/r/" + roomId
		params.RoomID = roomId
		params.RoomLink = roomLink
	}
	if clientId != "" {
		params.ClientID = clientId
	}
	if isInitiator {
		params.IsInitiator = "true"
	}
	return
}

func roomPage(w http.ResponseWriter, r *http.Request) {
	// Renders index.html or full.html.
	tpl := "index_template.html"
	roomId := mux.Vars(r)["roomid"]

	if room, ok := rooms[roomId]; ok {
		log.Printf("Room %s has state %v", roomId, rooms[roomId])
		if room.getOccupancy() >= 2 {
			log.Printf("Room %s is full", roomId)
			tpl = "full_template.html"
		}
		params, err := getRoomParameters(r, roomId, "", false)
		if err != nil {
			log.Printf("getRoomParameters: %v", err)
		}
		roomTemplate := template.Must(template.ParseFiles(tpl))
		err = roomTemplate.Execute(w, params)
		if err != nil {
			log.Printf("roomTemplate: %v", err)
		}
	}
}

func joinPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	roomId := mux.Vars(r)["roomid"]
	clientId := generateRandom(8)
	isLoopback := q.Get("debug") == "loopback"
	error, isInitiator, messages, _ := addClientToRoom(roomId, clientId, isLoopback)

	if error != "" {
		log.Printf("Error adding client to room: %s room_state %v", error, rooms[roomId])
		return
	}
	params, err := getRoomParameters(r, roomId, clientId, isInitiator)
	if err != nil {
		log.Printf("Error adding client to room: %v", err)
		return
	}
	log.Printf("User %s joined room %s", clientId, roomId)
	log.Printf("Room %s has state %v", roomId, rooms[roomId])

	params.Messages = messages
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result{"SUCCESS", params})
}

func leavePage(w http.ResponseWriter, r *http.Request) {
	roomId := mux.Vars(r)["roomid"]
	clientId := mux.Vars(r)["clientid"]
	if roomId != "" && clientId != "" {
		error, roomState := removeClientFromRoom(roomId, clientId)
		if error == "" {
			log.Printf("Room %s has state %s", roomId, roomState)
		}
	}
}

// The main UI page, renders the 'index_template.html' template.
func mainPage(w http.ResponseWriter, r *http.Request) {
	params, err := getRoomParameters(r, "", "", false)
	if err != nil {
		log.Printf("getRoomParameters: %v", err)
	}
	mainTemplate := template.Must(template.ParseFiles("index_template.html"))
	err = mainTemplate.Execute(w, params)
	if err != nil {
		log.Printf("mainTemplate: %v", err)
	}
}

func paramsPage(w http.ResponseWriter, r *http.Request) {
	params, err := getRoomParameters(r, "", "", false)
	if err != nil {
		log.Printf("getRoomParameters: %v", err)
	}
	_, err = json.Marshal(params)
	if err != nil {
		log.Printf("Unmarshal: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(params)
}

func iceConfigPage(w http.ResponseWriter, r *http.Request) {
	cfgs := makePcConfig("", ICE_SERVER_OVERRIDE)
	_, err := json.Marshal(cfgs)
	if err != nil {
		log.Printf("iceConfigPage: marshal: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(cfgs)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	r := mux.NewRouter()
	r.HandleFunc("/", mainPage).Methods("GET")
	r.HandleFunc("/join/{roomid}", joinPage).Methods("POST")
	r.HandleFunc("/leave/{roomid}/{clientid}", leavePage).Methods("POST")
	r.HandleFunc("/message/{roomid}/{clientid}", messagePage).Methods("POST")
	r.HandleFunc("/params", paramsPage).Methods("GET")
	r.HandleFunc("/v1alpha/iceconfig", iceConfigPage).Methods("POST")
	r.HandleFunc("/r/{roomid}", roomPage)
	// collider needs websocket support not available on appengine standard runtime
	if os.Getenv("GAE_ENV") != "standard" {
		// use collider locally
		c := collider.NewCollider("http://localhost:" + port)
		r.Handle("/ws", websocket.Handler(c.WsHandler))
		r.HandleFunc("/status", c.HttpStatusHandler)
		r.HandleFunc("/{roomid}/{clientid}", c.HttpHandler).Methods("POST", "DELETE")
		r.HandleFunc("/bye/{roomid}/{clientid}", c.HttpHandler)
		http.Handle("/css/", http.StripPrefix("/css/", http.FileServer(http.Dir("./css"))))
		http.Handle("/html/", http.StripPrefix("/html/", http.FileServer(http.Dir("./html"))))
		http.Handle("/images/", http.StripPrefix("/images/", http.FileServer(http.Dir("./images"))))
		http.Handle("/js/", http.StripPrefix("/js/", http.FileServer(http.Dir("./js"))))
	}
	http.Handle("/", r)
	log.Printf("listening on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

/*
 * WebRTC Demo
 *
 * This module demonstrates the WebRTC API by implementing a simple video chat app.
 *
 * Rewritten in Golang by Daniel G. Vargas
 * Based on https://github.com/webrtc/apprtc/blob/master/src/app_engine/apprtc.py (rev.1ee9435)
 * Look browser support on http://iswebrtcreadyyet.com/
 *
 */
package main

import (
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// Deprecated domains which we should to redirect to REDIRECT_URL.
	/*
	   REDIRECT_DOMAINS =  [
	     'goapprtc.appspot.com'
	   ]
	*/
	// URL which we should redirect to if matching in REDIRECT_DOMAINS.
	REDIRECT_URL = "https://goapprtc.appspot.com"

	ROOM_MEMCACHE_EXPIRATION_SEC = 60 * 60 * 24
	MEMCACHE_RETRY_LIMIT         = 100

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
	ICE_SERVER_OVERRIDE     = ""
	ICE_SERVER_BASE_URL     = "https://goapprtc.appspot.com"
	ICE_SERVER_URL_TEMPLATE = "%s/v1alpha/iceconfig?key=%s"
	/*
	   ICE_SERVER_API_KEY = os.GetVar("ICE_SERVER_API_KEY")
	   HEADER_MESSAGE = os.GetVar("HEADER_MESSAGE")
	   ICE_SERVER_URLS = [url for url in os.environ.get('ICE_SERVER_URLS', '').split(',') if url]

	   // Dictionary keys in the collider instance info constant.
	   WSS_INSTANCE_HOST_KEY = "host_port_pair"
	   WSS_INSTANCE_NAME_KEY = "vm_name"
	   WSS_INSTANCE_ZONE_KEY = "zone"
	   WSS_INSTANCES = [{
	       WSS_INSTANCE_HOST_KEY: 'apprtc-collider.herokuapp.com:443',
	       WSS_INSTANCE_NAME_KEY: 'wsserver-std',
	       WSS_INSTANCE_ZONE_KEY: 'us-central1-a'
	   }]

	   WSS_HOST_PORT_PAIRS = [ins[WSS_INSTANCE_HOST_KEY] for ins in WSS_INSTANCES]
	*/
	// memcache key for the active collider host.
	WSS_HOST_PORT_PAIRS      = ""
	WSS_HOST_ACTIVE_HOST_KEY = ""

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
	HEADER_MESSAGE = os.Getenv("HEADER_MESSAGE")
)

type Config struct {
	Url        string `json:"url,omitempty"`
	Credential string `json:"credential,omitempty"`
}

type ICE struct {
	IceServers []Config `json:"iceServers,omitempty"`
}

type Options struct {
	DtlsSrtpKeyAgreement bool
	googDscp             bool
	googIPv6             bool
}

type Constraints struct {
	Optional  interface{} `json:"optional"`
	Mandatory interface{} `json:"mandatory,omitempty"`
}

type MediaConstraints struct {
	Audio interface{} `json:"audio"`
	Video interface{} `json:"video"`
	Fake  interface{} `json:"fake"`
}

type SDP struct {
	Type      string `json:"type"`
	Sdp       string `json:"sdp,omitempty"`
	Id        string `json:"id,omitempty"`
	Candidate string `json:"candidate,omitempty"`
	Label     int    `json:"label,omitempty"`
}

// This database is to store the messages from the sender client when the
// receiver client is not ready to receive the messages.
// Use []byte instead of string for msg because
// the session description can be more than 500 characters.
type Message struct {
	Client_Id string `datastore:"client_id"`
	Msg       []byte `datastore:"msg"`
}

// All the data we store for a room
type Room struct {
	User1           string `datastore:"user1"`
	User2           string `datastore:"user2"`
	User1_Connected bool   `datastore:"user1_connected"`
	User2_Connected bool   `datastore:"user2_connected"`
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

func getDefaultStunServer(user_agent string) string {
	var default_stun_server string
	default_stun_server = "stun.l.google.com:19302"
	if strings.Contains(user_agent, "Firefox") {
		default_stun_server = "stun.services.mozilla.com"
	}
	return default_stun_server
}

func getPreferredAudioReceiveCodec() string {
	return "opus/48000"
}

func getPreferredAudioSendCodec(user_agent string) string {
	var preferred_audio_send_codec = ""
	if (strings.Contains(user_agent, "Android")) && (strings.Contains(user_agent, "Chrome")) {
		preferred_audio_send_codec = "ISAC/16000"
	}
	return preferred_audio_send_codec
}

func makePcConfig(stun_server, turn_server, ts_pwd string) interface{} {
	cfgs := make([]Config, 0)
	if turn_server != "" {
		cfgs = append(cfgs, Config{Url: "turn:" + turn_server, Credential: ts_pwd})
	}
	if stun_server != "" {
		cfgs = append(cfgs, Config{Url: "stun:" + stun_server})
	}
	return &ICE{IceServers: cfgs}
}

func makeLoopbackAnswer(message string) string {
	message = strings.Replace(message, `"offer"`, `"answer"`, -1)
	message = strings.Replace(message, "a=ice-options:google-ice\r\n", "", -1)
	return message
}

func makeMediaTrackConstraints(constraints string) interface{} {
	var track_constraints interface{}
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
			} /*else {
				panic("Ignoring malformed constraint: " + constraints)
			}*/
		}
		track_constraints = &Constraints{Optional: optl, Mandatory: mand}
	}
	return track_constraints
}

func makeMediaStreamConstraints(audio, video, firefoxFakeDevice string) interface{} {
	return &MediaConstraints{Audio: makeMediaTrackConstraints(audio), Video: makeMediaTrackConstraints(video), Fake: makeMediaTrackConstraints(firefoxFakeDevice)}
}

func makeOfferConstraints() interface{} {
	var constraints *Constraints
	constraints = &Constraints{Optional: []Options{}, Mandatory: map[string]string{}}
	return constraints
}

func (r *Room) getOccupancy() int {
	occupancy := 0
	if r.User1 != "" {
		occupancy += 1
	}
	if r.User2 != "" {
		occupancy += 1
	}
	return occupancy
}

func (r *Room) getOtherUser(user string) string {
	if user == r.User1 {
		return r.User2
	} else if user == r.User2 {
		return r.User1
	} else {
		return ""
	}
}

func (r *Room) hasUser(user string) bool {
	if (user == r.User1) || (user == r.User2) {
		return true
	} else {
		return false
	}
}

func (r *Room) addUser(user string) {
	if r.User1 == "" {
		r.User1 = user
	} else if r.User2 == "" {
		r.User2 = user
	} else {
		panic("room is full")
	}
}

func (r *Room) removeUser(user string) {
	if user == r.User2 {
		r.User2 = ""
		r.User2_Connected = false
	}
	if user == r.User1 {
		if r.User2 != "" {
			r.User1 = r.User2
			r.User1_Connected = r.User2_Connected
			r.User2 = ""
			r.User2_Connected = false
		} else {
			r.User1 = ""
			r.User1_Connected = false
		}
	}
}

func (r *Room) setConnected(user string) {
	if user == r.User1 {
		r.User1_Connected = true
	}
	if user == r.User2 {
		r.User2_Connected = true
	}
}

func (r *Room) isConnected(user string) bool {
	if user == r.User1 {
		return r.User1_Connected
	}
	if user == r.User2 {
		return r.User2_Connected
	}
	return false
}

/*Distp.ResponseWriter, r *http.Request) {
	b, _ := ioutil.ReadAll(r.Body)
	re := regexp.MustCompile("[0-9]+/[0-9]+")
	k := strings.Split(re.FindString(string(b)), "/")
	room_key := k[0]
	user := k[1]
	client_id := makeClientId(room_key, user)
	room := new(Room)
	err := datastore.Get(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
	if err != nil {
		c.Errorf("datastore: %v", err)
	}
	if room != nil && room.has_user(user) {
		other_user := room.get_other_user(user)
		room.remove_user(user)
		q := datastore.NewQuery("Message").Filter("client_id =", client_id)
		var messages []Message
		k, err := q.GetAll(c, &messages)
		if err != nil {
			c.Errorf("datastore: %v", err)
		}
		for i, _ := range messages {
			datastore.Delete(c, datastore.NewKey(c, k[i].Kind(), k[i].StringID(), k[i].IntID(), nil))
			c.Infof("Deleted the saved message for " + client_id)
		}
		if room.get_occupancy() > 0 {
			datastore.Put(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
		} else {
			datastore.Delete(c, datastore.NewKey(c, "Room", room_key, 0, nil))
		}
		c.Infof("User %s removed from room %s", user, room_key)
		c.Infof("Room %s has state %v", room_key, room)
		if other_user != "" && other_user != user {
			channel.Send(c, room_key+"/"+other_user, `{"type": "bye"}`)
			c.Infof("Sent BYE to %s", other_user)
		}
		c.Warningf("User %s disconnected from room %s", user, room_key)
	}
}

func connectPage(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	b, _ := ioutil.ReadAll(r.Body)
	re := regexp.MustCompile("[0-9]+/[0-9]+")
	k := strings.Split(re.FindString(string(b)), "/")
	room_key := k[0]
	user := k[1]
	client_id := make_client_id(room_key, user)
	room := new(Room)
	err := datastore.Get(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
	if err != nil {
		c.Errorf("datastore: %v", err)
	}
	// Check if room has user in case that disconnect message comes before
	// connect message with unknown reason, observed with local AppEngine SDK.
	if room != nil && room.has_user(user) {
		room.set_connected(user)
		datastore.Put(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
		q := datastore.NewQuery("Message").Filter("client_id =", client_id)
		var messages []Message
		k, err := q.GetAll(c, &messages)
		if err != nil {
			c.Errorf("datastore: %v", err)
		}
		for i, msg := range messages {
			channel.Send(c, client_id, string(msg.Msg))
			c.Infof("Delivered saved message to " + client_id)
			datastore.Delete(c, datastore.NewKey(c, k[i].Kind(), k[i].StringID(), k[i].IntID(), nil))
		}
		c.Infof("User %s connected to room %s", user, room_key)
		c.Infof("Room %s has state %v", room_key, room)
	} else {
		c.Warningf("Unexpected Connect Message to room %s", room_key)
	}
}
*/
/*
func messagePage(w http.ResponseWriter, r *http.Request) {
	//c := appengine.NewContext(r)
	room_key := r.URL.Query().Get("r")
	user := r.URL.Query().Get("u")
	m, _ := ioutil.ReadAll(r.Body)
	msg := string(m)
	room := new(Room)
	client_id := make_client_id(room_key, user)
	datastore.Get(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
	if room != nil {
		var message SDP
		json.Unmarshal(m, &message)
		other_user := room.get_other_user(user)
		if message.Type == "bye" {
			// This would remove the other_user in loopback test too.
			// So check its availability before forwarding Bye message.
			room.remove_user(user)
			q := datastore.NewQuery("Message").Filter("client_id =", client_id)
			var messages []Message
			k, err := q.GetAll(c, &messages)
			if err != nil {
				c.Errorf("datastore: %v", err)
			}
			for i, _ := range messages {
				datastore.Delete(c, datastore.NewKey(c, k[i].Kind(), k[i].StringID(), k[i].IntID(), nil))
				c.Infof("Deleted the saved message for " + client_id)
			}
			if room.get_occupancy() > 0 {
				datastore.Put(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
			} else {
				datastore.Delete(c, datastore.NewKey(c, "Room", room_key, 0, nil))
			}
			c.Infof("User %s quit from room %s", user, room_key)
			c.Infof("Room %s has state %v", room_key, room)
		}
		if other_user != "" && room.has_user(other_user) {
			if message.Type == "offer" {
				// Special case the loopback scenario.
				if other_user == user {
					msg = make_loopback_answer(msg)
				}
			}
			if room.is_connected(other_user) {
				channel.Send(c, make_client_id(room_key, other_user), msg)
				c.Infof("Delivered message to user %s", other_user)
			} else {
				_msg := Message{
					Client_Id: make_client_id(room_key, other_user),
					Msg:       []byte(msg),
				}
				_, err := datastore.Put(c, datastore.NewIncompleteKey(c, "Message", nil), &_msg)
				if err != nil {
					c.Errorf("datastore: %v", err)
				}
				c.Infof("Saved message for user %s", other_user)
			}
		}
	} else {
		c.Warningf("Unknown room %s", room_key)
	}
}
*/

func makePcConstraints(dtls, dscp, ipv6 string) interface{} {
	var constraints *Constraints
	var _dtls, _dscp, _ipv6 bool
	constraints = &Constraints{Optional: []Options{}}
	if strings.ToLower(dtls) == "true" {
		_dtls = true
	}
	if strings.ToLower(dscp) == "true" {
		_dscp = true
	}
	if strings.ToLower(ipv6) == "true" {
		_ipv6 = true
	}
	constraints = &Constraints{Optional: []Options{{DtlsSrtpKeyAgreement: _dtls, googDscp: _dscp, googIPv6: _ipv6}}}
	return constraints
}

func maybeUseHttpsHostUrl(r *http.Request) string {
	q := r.URL.Query()
	if q.Get("wstls") == "true" && r.URL.Scheme == "http" {
		// Assume AppRTC is running behind a stunnel proxy and fix base URL.
		return strings.Replace(r.URL.Host, "http:", "https:", 1)
	}
	return r.URL.Host
}

func getWssParameters(r *http.Request) (wssUrl, wssPostUrl string) {
	q := r.URL.Query()
	wssHostPortPair := q.Get("wshpp")
	wssTls := q.Get("wstls")

	if wssHostPortPair == "" {
		wssActiveHost := WSS_HOST_ACTIVE_HOST_KEY
		if strings.Contains(WSS_HOST_PORT_PAIRS, wssActiveHost) {
			wssHostPortPair = WSS_HOST_PORT_PAIRS
		} else {
			// logging warn
			wssHostPortPair = strings.Split(WSS_HOST_PORT_PAIRS, ",")[0]
		}
	}
	if wssTls == "false" {
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
func getRoomParameters(r *http.Request, roomId, clientId string, isInitiator bool) (params map[string]interface{}, err error) {
	q := r.URL.Query()
	// Append strings to this list to have them thrown up in message boxes. This
	// will also cause the app to fail.
	errorMessages := []string{}
	var message string
	// Get the base url without arguments.
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
		errorMessages = append(errorMessages, message)
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
		errorMessages = append(errorMessages, message)
	}

	/*
		audioSendCodec := q.Get("asc")
		if audioSendCodec == "" {
			audioSendCodec = getPreferredAudioSendCodec(userAgent)
		}
		audioReceiveCodec := q.Get("arc")
		if audioReceiveCodec == "" {
			audioReceiveCodec = getPreferredAudioReceiveCodec()
		}

			stereo := false
			if q.Get("stereo") != "" {
				if q.Get("stereo") == "true" {
					stereo = true
				}
			}
	*/

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

	/*
		unittest := q.Get("unittest")
		if unittest != "" {
			// Always create a new room for the unit tests.
			roomKey = generateRandom(8)
		}
			if roomKey == "" {
				roomKey = generateRandom(8)
				q.Set("r", roomKey)
				r.URL.RawQuery = q.Encode()
				redirect := r.URL.String()
				http.Redirect(w, r, redirect, http.StatusFound)
				//c.Infof("Redirecting visitor to base URL to " + redirect)
				return
			}
	*/
	//room := new(Room)
	//err := datastore.Get(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
	//var user string
	//var initiator int
	/*
			if debug != "full" {
				// New room.
				user = generateRandom(8)
				room.addUser(user)
				if debug != "loopback" {
					initiator = 0
				} else {
					room.addUser(user)
					initiator = 1
				}

				//	_, err := datastore.Put(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
				if err != nil {
					//	c.Errorf("datastore: %v", err)
				}
			} else if room != nil && room.getOccupancy() == 1 && debug != "full" {
				// 1 occupant.
				user = generateRandom(8)
				room.addUser(user)
				//_, err := datastore.Put(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
				if err != nil {
					//		c.Errorf("datastore: %v", err)
				}
				initiator = 1
			} else {
				// 2 occupants (full).
				var template = template.Must(template.ParseFiles("full.html"))
				template.Execute(w, map[string]string{})
				//c.Infof("Room " + room_key + " is full")
				return
			}
		q.Set("r", roomKey)
		r.URL.RawQuery = q.Encode()
		//roomLink := r.URL.String()
		//turnUrl := "https://goapprtc.appspot.com/"
		//turn_url = turn_url + "turn?" + "username=" + user + "&key=4080218913"
		//token, _ := channel.Create(c, make_client_id(room_key, user))
	*/
	pcConfig := makePcConfig(iceTransports, ICE_SERVER_OVERRIDE, "")
	pcConstraints := makePcConstraints(dtls, dscp, ipv6)
	//offerOptions := makeOfferConstraints()
	mediaConstraints := makeMediaStreamConstraints(audio, video, firefoxFakeDevice)
	log.Printf("Applying media constraints: %v", mediaConstraints)

	params = map[string]interface{}{
		"header_message": HEADER_MESSAGE,
		"error_messages": errorMessages,
		//	"warning_messages":         warningMessages,
		"is_loopback":    debug == "loopback",
		"pc_config":      pcConfig,
		"pc_constraints": pcConstraints,
		//"offer_options":            offerOptions,
		"media_constraints": mediaConstraints,
		//"ice_server_url":           iceServerUrl,
		"ice_server_transports": iceServerTransports,
		"include_loopback_js":   includeLoopbackJs,
		//"wss_url":                  wssUrl,
		//"wss_post_url":             wssPostUrl,
		//"bypass_join_confirmation": bypassJoinConfirmation,
		//"audio_receive_codec":      audioReceiveCodec,
		//"version_info":		   getVersionInfo(),
	}
	/*
		if roomId != "" {
			roomLink := maybeUseHttpsHostUrl(r) + "/r/" + roomId
			room_link = appendUrlArguments(r, roomLink)
			params["room_id"] = roomId
			params["room_link"] = roomLink
		}
		if clientId != "" {
			params["client_id"] = clientId
		}
		if isInitiator {
			params["is_initiator"] = is_initiator
		}
	*/
	return
}

/*
func checkIfRedirect(w http.ResponseWriter, r *http.Request) {
	var parsedArgs string
	if strings.Contains(r.Header.Get("Host"),REDIRECT_DOMAINS) {
		q := r.URL.Query()
		for a := range url.Values {
			p = "=" + q.Get(a)
			if parsedArgs == "" {
				parsedArgs += "?"
			} else {
				parsedArgs += "&"
			}
			parsedArgs += a + p
		}
		redirectUrl:= REDIRECT_URL + r.Path + parsedAparsedArgs
		http.Redirect(w, r, redirectUrl , 301)
	}
	return
}
*/

// The main UI page, renders the 'index_template.html' template.
func mainPage(w http.ResponseWriter, r *http.Request) {
	//checkIfRedirect(r)
	params, err := getRoomParameters(r, "", "", false)
	if err != nil {
		log.Printf("getRoomParameters: %v", err)
	}
	mainTemplate := template.Must(template.ParseFiles("html/index_template.html"))
	err = mainTemplate.Execute(w, params)
	if err != nil {
		log.Printf("mainTemplate: %v", err)
	}
	//c.Infof("User %s added to room %s", user, room_key)
	//c.Infof("Room %s has state %v", room_key, room)
}

/*
 *  CORS WORKAROUND: computeengineondemand access-control-allow-origin: https://apprtc.appspot.com
 */
/*
type Turn struct {
	Username string   `json:"username"`
	Password string   `json:"password"`
	Uris     []string `json:"uris"`
}

func turn(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	key := r.URL.Query().Get("key")
	c := appengine.NewContext(r)
	fetcher := urlfetch.Client(c)
	u, _ := url.Parse("https://computeengineondemand.appspot.com/turn")
	q := u.Query()
	if username != "" {
		q.Set("username", username)
	}
	if key != "" {
		q.Set("key", key)
	}
	u.RawQuery = q.Encode()
	res, err := fetcher.Get(u.String())
	if err != nil {
		c.Errorf("Fetch: %v", err)
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		c.Errorf("ReadAll: %v", err)
	}
	var data Turn
	err = json.Unmarshal(body, &data)
	if err != nil {
		c.Errorf("Unmarshal: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "https://goapprtc.appspot.com")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	if err := enc.Encode(data); err != nil {
		c.Errorf("Encode: %v", err)
	}
}

func iceConfigurationPage(w http.ResponseWriter, r *http.Request) {
	cfgs := make([]Config, 0)
	if turn_server != "" {
		cfgs = append(cfgs, Config{Url: "turn:" + turn_server, Credential: ts_pwd})
	}
	if stun_server != "" {
		cfgs = append(cfgs, Config{Url: "stun:" + stun_server})
	}
	return &ICE{IceServers: cfgs}

	var data ICE
	err = json.Unmarshal(body, &data)
	if err != nil {
		c.Errorf("Unmarshal: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	//w.Header().Set("Access-Control-Allow-Origin", "https://goapprtc.appspot.com")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	if err := enc.Encode(data); err != nil {
		c.Errorf("Encode: %v", err)
	}
}
*/
func main() {
	http.HandleFunc("/", mainPage)
	//http.HandleFunc("/join/", joinPage)
	//http.HandleFunc("/leave/", leavePage)
	//http.HandleFunc("/message/", messagePage)
	//http.HandleFunc("/params", paramsPage)
	//http.HandleFunc("/v1alpha/iceconfig", iceConfigurationPage)
	//http.HandleFunc("/r/", roomPage)
	// collider need websocket support not available on appengine standard
	/*
		if os.Getenv("GAE_ENV") != "standard" {
			m.HandleFunc("/ws", collider.Handler).Methods("POST")
		}
	*/
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("listening on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

/*
 * WebRTC Demo
 *
 * This module demonstrates the WebRTC API by implementing a simple video chat app.
 * 
 * Rewritten in Golang by Daniel G. Vargas
 * Based on http://webrtc.googlecode.com/svn/trunk/samples/js/apprtc/apprtc.py (rev.4950)
 * Look browser support on http://iswebrtcreadyyet.com/
 *
 */
package main

import (
	"encoding/json"
	"html/template"
	"io/ioutil"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"appengine"
	"appengine/channel"
	"appengine/datastore"
	"github.com/gorilla/mux"
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
}
type Constraints struct {
	Optional  []Options `json:"optional"`
	Mandatory Mand      `json:"mandatory,omitempty"`
}
type Mand struct {
	MinWidth  string `json:"minWidth,omitempty"`
	MaxWidth  string `json:"maxWidth,omitempty"`
	MinHeight string `json:"minHeight,omitempty"`
	MaxHeight string `json:"maxHeight,omitempty"`
}
type MediaConstraints struct {
	Audio interface{} `json:"audio,omitempty"`
	Video interface{} `json:"video"`
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

func generate_random(length int) string {
	word := ""
	for i := 0; i < length; i++ {
		rand.Seed(time.Now().UTC().UnixNano())
		word += strconv.Itoa(rand.Intn(10))
	}
	return word
}

func randomInt(min int, max int) int {
	var bytes int
	bytes = min + rand.Intn(max)
	return int(bytes)
}

func sanitize(key string) string {
	re := regexp.MustCompile("[^a-zA-Z0-9]")
	return re.ReplaceAllString(key, "-")
}

func make_client_id(room, user string) string {
	return room + "/" + user
}

func get_default_stun_server(user_agent string) string {
	var default_stun_server string
	default_stun_server = "stun.l.google.com:19302"
	if strings.Contains(user_agent, "Firefox") {
		default_stun_server = "stun.services.mozilla.com"
	}
	return default_stun_server
}

func get_preferred_audio_receive_codec() string {
	return "opus/48000"
}

func get_preferred_audio_send_codec(user_agent string) string {
	var preferred_audio_send_codec = ""
	if (strings.Contains(user_agent, "Android")) && (strings.Contains(user_agent, "Chrome")) {
		preferred_audio_send_codec = "ISAC/16000"
	}
	return preferred_audio_send_codec
}

func make_pc_config(stun_server, turn_server, ts_pwd string) interface{} {
	cfgs := make([]Config, 0)
	if turn_server != "" {
		cfgs = append(cfgs, Config{Url: "turn:" + turn_server, Credential: ts_pwd})
	}
	if stun_server != "" {
		cfgs = append(cfgs, Config{Url: "stun:" + stun_server})
	}

	return &ICE{IceServers: cfgs}
}

func make_loopback_answer(message string) string {
	message = strings.Replace(message, `"offer"`, `"answer"`, -1)
	message = strings.Replace(message, "a=ice-options:google-ice\r\n", "", -1)
	return message
}

func make_media_track_constraints(constraints string) interface{} {
	type c struct {
		Mandatory interface{}
		Optional  []string
	}
	var track_constraints interface{}
	if constraints == "" || strings.ToLower(constraints) == "true" {
		track_constraints = true
	} else if strings.ToLower(constraints) == "false" {
		track_constraints = false
	} else {
		track_constraints = &c{Mandatory: []string{}, Optional: []string{}}
		/*constraint := strings.Split(track_constraints, "=")
		if len(constraint) != 2 {
			panic("Ignoring malformed constraint: ", constraint)
		}
		if strings.Contains(constraint[0],"goog"){
		    track_constraints["optional"] = append(c, {constraint[0]: constraint[1]})
		  } else {
		    track_constraints["mandatory"][constraint[0]] = constraint[1]
		  }*/
	}
	return track_constraints
}

func make_media_stream_constraints(audio, video string) interface{} {
	return &MediaConstraints{Audio: make_media_track_constraints(audio), Video: make_media_track_constraints(video)}
}

func make_pc_constraints(compat string) interface{} {
	var constraints *Constraints
	constraints = &Constraints{Optional: []Options{}}
	// For interop with FireFox. Enable DTLS in peerConnection ctor.
	if strings.ToLower(compat) == "true" {
		constraints = &Constraints{Optional: []Options{{DtlsSrtpKeyAgreement: true}}}
		// Disable DTLS in peerConnection ctor for loopback call. The value
		// of compat is false for loopback mode.
	} else {
		constraints = &Constraints{Optional: []Options{{DtlsSrtpKeyAgreement: false}}}
	}
	return constraints
}

func make_offer_constraints() interface{} {
	var constraints *Constraints
	constraints = &Constraints{Optional: []Options{}}
	return constraints
}

func (r *Room) get_occupancy() int {
	occupancy := 0
	if r.User1 != "" {
		occupancy += 1
	}
	if r.User2 != "" {
		occupancy += 1
	}
	return occupancy
}

func (r *Room) get_other_user(user string) string {
	if user == r.User1 {
		return r.User2
	} else if user == r.User2 {
		return r.User1
	} else {
		return ""
	}
}

func (r *Room) has_user(user string) bool {
	if (user == r.User1) || (user == r.User2) {
		return true
	} else {
		return false
	}
}

func (r *Room) add_user(user string) {
	if r.User1 == "" {
		r.User1 = user
	} else if r.User2 == "" {
		r.User2 = user
	} else {
		panic("room is full")
	}
}

func (r *Room) remove_user(user string) {
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

func (r *Room) set_connected(user string) {
	if user == r.User1 {
		r.User1_Connected = true
	}
	if user == r.User2 {
		r.User2_Connected = true
	}
}

func (r *Room) is_connected(user string) bool {
	if user == r.User1 {
		return r.User1_Connected
	}
	if user == r.User2 {
		return r.User2_Connected
	}
	return false
}

func disconnected_page(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	b, _ := ioutil.ReadAll(r.Body)
	re := regexp.MustCompile("[0-9]+/[0-9]+")
	k := strings.Split(re.FindString(string(b)), "/")
	room_key:= k[0]
	user := k[1]
	client_id := make_client_id(room_key, user)
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
		c.Infof("Room %s has state %q", room_key, room)
		if other_user != "" && other_user != user {
			channel.Send(c, room_key+"/"+other_user, `{"type": "bye"}`)
			c.Infof("Sent BYE to %s", other_user)
		}
		c.Warningf("User %s disconnected from room %s", user, room_key)
	}
}

func connect_page(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	b, _ := ioutil.ReadAll(r.Body)
	re := regexp.MustCompile("[0-9]+/[0-9]+")
	k := strings.Split(re.FindString(string(b)), "/")
	room_key:= k[0]
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
		c.Infof("Room %s has state %q", room_key, room)
	} else {
		c.Warningf("Unexpected Connect Message to room %s", room_key)
	}
}

func message_page(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
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
			c.Infof("Room %s has state %q", room_key, room)
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

// The main UI page, renders the 'index.html' template.
func main_page(w http.ResponseWriter, r *http.Request) {
	// Renders the main page. When this page is shown, we create a new
	// channel to push asynchronous updates to the client.

	// Append strings to this list to have them thrown up in message boxes. This
	// will also cause the app to fail.
	c := appengine.NewContext(r)
	q := r.URL.Query()
	error_messages := []string{}
	var message string
	user_agent := r.UserAgent()
	room_key := sanitize(q.Get("r"))
	stun_server := q.Get("ss")
	if stun_server == "" {
		stun_server = get_default_stun_server(user_agent)
	}
	turn_server := q.Get("ts")

	ts_pwd := q.Get("tp")

	/*
	 * Use "audio" and "video" to set the media stream constraints. Defined here:
	 * http://dev.w3.org/2011/webrtc/editor/getusermedia.html#idl-def-MediaStreamConstraints
	 *
	 * "true" and "false" are recognized and interpreted as bools, for example:
	 * "?audio=true&video=false" (Start an audio-only call.)
	 * "?audio=false" (Start a video-only call.)
	 * If unspecified, the stream constraint defaults to True.
	 *
	 * To specify media track constraints, pass in a comma-separated list of
	 * key/value pairs, separated by a "=". Examples:
	 *   "?audio=googEchoCancellation=false,googAutoGainControl=true"
	 *   (Disable echo cancellation and enable gain control.)
	 *
	 *   "?video=minWidth=1280,minHeight=720,googNoiseReduction=true"
	 *   (Set the minimum resolution to 1280x720 and enable noise reduction.)
	 *
	 * Keys starting with "goog" will be added to the "optional" key; all others
	 * will be added to the "mandatory" key.
	 *
	 * The audio keys are defined here:
	 * https://code.google.com/p/webrtc/source/browse/trunk/talk/app/webrtc/localaudiosource.cc
	 *
	 * The video keys are defined here:
	 * https://code.google.com/p/webrtc/source/browse/trunk/talk/app/webrtc/videosource.cc
	 */
	audio := q.Get("audio")
	video := q.Get("video")
	if strings.ToLower(q.Get("hd")) == "true" {
		if video != "" {
			message = `The "hd" parameter has overridden video=` + string(video)
			c.Errorf(message)
			error_messages = append(error_messages, message)
		}
		video = "minWidth=1280,minHeight=720"
	}
	if q.Get("minre") != "" || q.Get("maxre") != "" {
		message = `The "minre" and "maxre" parameters are no longer supported.\n
		    Use "video" instead.`
		c.Errorf(message)
		error_messages = append(error_messages, message)
	}
	audio_send_codec := q.Get("asc")
	if audio_send_codec == "" {
		audio_send_codec = get_preferred_audio_send_codec(user_agent)
	}
	audio_receive_codec := q.Get("arc")
	if audio_receive_codec == "" {
		audio_receive_codec = get_preferred_audio_receive_codec()
	}
	// Set stereo to false by default.
	stereo := false
	if q.Get("stereo") != "" {
		if q.Get("stereo") == "true" {
			stereo = true
		}
	}
	// Set compat to true by default.
	compat := "true"
	if q.Get("compat") != "" {
		compat = q.Get("compat")
	}
	debug := q.Get("debug")
	if debug == "loopback" {
		// Set compat to false as DTLS does not work for loopback.
		compat = "false"
	}
	unittest := q.Get("unittest")
	if unittest != "" {
		// Always create a new room for the unit tests.
		room_key = generate_random(8)
	}
	if room_key == "" {
		room_key = generate_random(8)
		q.Set("r",room_key)
		r.URL.RawQuery = q.Encode()
		redirect:= r.URL.String()
		http.Redirect(w, r, redirect, http.StatusFound)
		c.Infof("Redirecting visitor to base URL to " + redirect)
		return
	}
	room := new(Room)
	err := datastore.Get(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
	var user string
	var initiator int
	if err == datastore.ErrNoSuchEntity && debug != "full" {
		// New room.
		user = generate_random(8)
		room.add_user(user)
		if debug != "loopback" {
			initiator = 0
		} else {
			room.add_user(user)
			initiator = 1
		}
		_, err := datastore.Put(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
		if err != nil {
			c.Errorf("datastore: %v", err)
		}
	} else if room != nil && room.get_occupancy() == 1 && debug != "full" {
		// 1 occupant.
		user = generate_random(8)
		room.add_user(user)
		_, err := datastore.Put(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
		if err != nil {
			c.Errorf("datastore: %v", err)
		}
		initiator = 1
	} else {
		// 2 occupants (full).
		var template = template.Must(template.ParseFiles("full.html"))
		template.Execute(w, map[string]string{})
		c.Infof("Room " + room_key + " is full")
		return
	}

	q.Set("r",room_key)
	r.URL.RawQuery = q.Encode()
	room_link := r.URL.String()
	turn_url := "https://computeengineondemand.appspot.com/"
	turn_url = turn_url + "turn?" + "username=" + user + "&key=4080218913"
	token, _ := channel.Create(c, make_client_id(room_key, user))
	pc_config := make_pc_config(stun_server, turn_server, ts_pwd)
	pc_constraints := make_pc_constraints(compat)
	offer_constraints := make_offer_constraints()
	media_constraints := make_media_stream_constraints(audio, video)
	c.Warningf(audio, video,media_constraints)
	c.Infof("Applying media constraints: %s", media_constraints)
	var target_page string
	if unittest != "" {
		target_page = "test/test_" + unittest + ".html"
	} else {
		target_page = "index.html"
	}
	mainTemplate := template.Must(template.ParseFiles(target_page))
	err = mainTemplate.Execute(w, map[string]interface{}{
		"error_messages":      error_messages,
		"token":               token,
		"me":                  user,
		"room_key":            room_key,
		"room_link":           room_link,
		"initiator":           initiator,
		"pc_config":           pc_config,
		"pc_constraints":      pc_constraints,
		"offer_constraints":   offer_constraints,
		"media_constraints":   media_constraints,
		"turn_url":            turn_url,
		"stereo":              stereo,
		"audio_send_codec":    audio_send_codec,
		"audio_receive_codec": audio_receive_codec,
	})
	if err != nil {
		c.Errorf("mainTemplate: %v", err)
	}
	c.Infof("User %s added to room %s", user, room_key)
	c.Infof("Room %s has state %q", room_key, room)
}

func init() {
	m := mux.NewRouter()
	m.HandleFunc("/", main_page).Methods("GET")
	m.HandleFunc("/message", message_page).Methods("POST")
	m.HandleFunc("/_ah/channel/connected/", connect_page).Methods("POST")
	m.HandleFunc("/_ah/channel/disconnected/", disconnected_page).Methods("POST")
	http.Handle("/", m)
}

/*
 * WebRTC Demo
 *
 * This module demonstrates the WebRTC API by implementing a simple video chat app.
 *
 * Based on http://webrtc.googlecode.com/svn/trunk/samples/js/apprtc/apprtc.py
 * Look browser support on http://iswebrtcreadyyet.com/
 *
 */
package main

import (
	//"errors"
	"html/template"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"encoding/json"
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
type Constraints2 struct {
	Optional  []Options `json:"optional"`
}
type Mand struct {
	MinWidth  string `json:"minWidth,omitempty"`
	MaxWidth  string `json:"maxWidth,omitempty"`
	MinHeight string `json:"minHeight,omitempty"`
	MaxHeight string `json:"maxHeight,omitempty"`
}
type MediaConstraints struct {
	Audio bool        `json:"audio,omitempty"`
	Video Constraints `json:"video"`
}
type SDP struct {
	Type      string `json:"type"`
	Sdp       string `json:"sdp,omitempty"`
	Id        string `json:"id,omitempty"`
	Candidate string `json:"candidate,omitempty"`
	Label     int    `json:"label,omitempty"`
}

func generate_random(length int) string {
	word := ""
	for i := 0; i < length; i++ {
		rand.Seed(time.Now().UTC().UnixNano()) // constant time on appengine =(
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
		cfgs = append(cfgs, Config{Url: "turn:"+turn_server, Credential: ts_pwd})
	}
	if stun_server != "" {
		cfgs = append(cfgs, Config{Url: "stun:"+stun_server})
	}

	return &ICE{IceServers: cfgs}
}

func make_loopback_answer(message string) string {
	message = strings.Replace(message, `"offer"`, `"answer"`, -1)
	message = strings.Replace(message, "a=ice-options:google-ice\r\n", "", -1)
	return message
}

func make_media_constraints(cxt appengine.Context, media, min_re, max_re string) interface{} {
	//mediaConstraints = {"audio":true,"video":"[]"};
	var a, b, c, d string
	// Media: audio:audio only; video:video only; (default):both.
	if strings.ToLower(media) != "audio" {
		if min_re != "" {
			min_sizes := strings.Split(min_re, "x")
			if len(min_sizes) == 2 {
				a = min_sizes[0]
				b = min_sizes[1]
			} else {
				cxt.Infof("Ignored invalid max_re:" + min_re)
			}
		}
		if max_re != "" {
			max_sizes := strings.Split(max_re, "x")
			if len(max_sizes) == 2 {
				c = max_sizes[0]
				d = max_sizes[1]
			} else {
				cxt.Infof("Ignored invalid max_re:" + max_re)
			}
		}
	}
	return &MediaConstraints{Audio: true, Video: Constraints{Mandatory: Mand{MinWidth: a, MinHeight: b, MaxWidth: c, MaxHeight: d}, Optional: []Options{}}}
}

func make_pc_constraints(compat string) interface{} {
	var constraints *Constraints
	constraints = &Constraints{Optional: []Options{}}
	// For interop with FireFox. Enable DTLS in peerConnection ctor.
	if strings.ToLower(compat) == "true" {
		constraints = &Constraints{Optional: []Options{{DtlsSrtpKeyAgreement: true}}}
	}
	return constraints
}

func make_offer_constraints() interface{} {
	var constraints *Constraints
	constraints = &Constraints{Optional: []Options{}}
	return constraints
}

type Message struct {
	Client_Id string
	Msg       string
}

type Room struct {
	User1           string
	User2           string
	User1_Connected bool
	User2_Connected bool
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
	//messages = datastore.NewQuery("Messages").Filter("client_id =", client_id)
	/*var messages []Message
	for _, message := range messages {
		channel.Send(c, client_id, message.Msg)
		c.Infof("Delivered saved message to " + client_id)
		//datastore.delete()
	}
	*/
	if user == r.User2 {
		r.User2 = ""
		r.User2_Connected = false
	}
	if user == r.User1 {
		r.User1 = r.User2
		r.User1_Connected = r.User2_Connected
		r.User2 = ""
		r.User2_Connected = false
	} else {
		r.User1 = ""
		r.User1_Connected = false
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
	client_id, _ := ioutil.ReadAll(r.Body)
	k := strings.Split(strings.Split(string(client_id), "=")[1],"/")
	var room_key string
	var user string
	if len(k) == 2 {
		room_key = k[0]
		user = k[1]
	}
	room := new(Room)
	err := datastore.Get(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
	if err != nil {
		c.Errorf("datastore: %v", err)
	}
	if room != nil && room.has_user(user) {
		other_user := room.get_other_user(user)
		room.remove_user(user)
		if room.get_occupancy() > 0 {
			datastore.Put(c, datastore.NewKey(c, "Room", room_key, 0, nil),room)
		} else {
			datastore.Delete(c, datastore.NewKey(c, "Room", room_key, 0, nil))
		}
		c.Infof("User %s removed from room %s", user, room_key)
		c.Infof("Room %s has state %q", room_key, room)
		if other_user != "" && other_user != user {
			channel.Send(c, room_key+"/"+other_user, `{"type": "bye"}`)
			c.Infof("Sent BYE to %s", other_user)
		}
		c.Infof("User %s disconnected from room %s", user, room_key)
	}
}

func connect_page(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	client_id, _ := ioutil.ReadAll(r.Body)
	k := strings.Split(strings.Split(string(client_id), "=")[1],"/")
	var room_key string
	var user string
	if len(k) == 2 {
		room_key = k[0]
		user = k[1]
	}
	room := new(Room)
	err := datastore.Get(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
	if err != nil {
		c.Errorf("datastore: %v", err)
	}
	if room != nil && room.has_user(user) {
		room.set_connected(user)
		datastore.Put(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
		//messages = datastore.NewQuery("Messages").Filter("client_id =", client_id)
		/*
		var messages []Message
		for _, message := range messages {
			channel.Send(c, client_id, message.Msg)
			c.Infof("Delivered saved message to " + client_id)
			//datastore.delete()
		}
		*/
		c.Infof("User %s connected to room %s", user, room_key)
		c.Infof("Room %s has state %q", room_key, room)
	} else {
		c.Infof("Unexpected Connect Message to room %s", room_key)
	}
}

func message_page(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	req, _ := url.Parse(r.RequestURI)
	room_key := req.Query().Get("r")
	user := req.Query().Get("u")
	m, _ := ioutil.ReadAll(r.Body)
	msg := string(m)
	room := new(Room)
	datastore.Get(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
	if room != nil {
		var message SDP
		json.Unmarshal(m, &message)
		other_user := room.get_other_user(user)
		if message.Type == "bye" {
			// This would remove the other_user in loopback test too.
			// So check its availability before forwarding Bye message.
			room.remove_user(user)
			if room.get_occupancy() > 0 {
				datastore.Put(c, datastore.NewKey(c, "Room", room_key, 0, nil),room)
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
				channel.Send(c, room_key+"/"+other_user, msg)
				c.Infof("Delivered message to user %s", other_user)
			} else {
				//new_message := Message(room_key+"/"+u, msg)
				//datastore.put()
				c.Infof("Saved message for user %s", user)
			}
		}
	} else {
		c.Infof("Unknown room %s", room_key)
	}
}

func main_page(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	u, _ := url.Parse(r.RequestURI)
	q := u.Query()
	base_url := "http://" + r.Host
	user_agent := r.UserAgent()
	room_key := sanitize(q.Get("r"))
	debug := q.Get("debug")
	unittest := q.Get("unittest")
	stun_server := q.Get("ss")
	turn_server := q.Get("ts")
	min_re := q.Get("minre")
	max_re := q.Get("maxre")
	audio_send_codec := q.Get("asc")
	if audio_send_codec == "" {
		audio_send_codec = get_preferred_audio_send_codec(user_agent)
	}
	audio_receive_codec := q.Get("arc")
	if audio_receive_codec == "" {
		audio_receive_codec = get_preferred_audio_receive_codec()
	}
	hd_video := q.Get("hd")
	turn_url := "https://computeengineondemand.appspot.com/"
	if strings.ToLower(hd_video) == "true" {
		min_re = "1280x720"
	}
	ts_pwd := q.Get("tp")
	media := q.Get("media")
	compat := "true"
	if q.Get("compat") != "" {
		compat = q.Get("compat")
	}
	if debug == "loopback" {
		compat = "false"
	}
	stereo := false
	if q.Get("stereo") != "" {
		if q.Get("stereo") == "true" {
			stereo = true
		}
	}
	if stun_server == "" {
		stun_server = get_default_stun_server(user_agent)
	}
	if unittest != "" {
		room_key = generate_random(8)
	}
	if room_key == "" {
		room_key = generate_random(8)
		redirect := "/?r=" + room_key
		redirect = base_url + redirect
		http.Redirect(w, r, redirect, http.StatusFound)
		c.Infof("Redirecting visitor to base URL to " + redirect)
		return
	}
	room := new(Room)
	err := datastore.Get(c, datastore.NewKey(c, "Room", room_key, 0, nil), room)
	if err != nil {
		c.Errorf("datastore: %v", err)
	}
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
	room_link := base_url + r.RequestURI
	turn_url = turn_url + "turn?" + "username=" + user + "&key=4080218913"
	token, _ := channel.Create(c, room_key+"/"+user)
	pc_config := make_pc_config(stun_server, turn_server, ts_pwd)
	pc_constraints := make_pc_constraints(compat)
	offer_constraints := make_offer_constraints()
	media_constraints := make_media_constraints(c, media, min_re, max_re)
	var target_page string
	if unittest != "" {
		target_page = "test/test_" + unittest + ".html"
	} else {
		target_page = "index.html"
	}
	mainTemplate := template.Must(template.ParseFiles(target_page))
	err = mainTemplate.Execute(w, map[string]interface{}{
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

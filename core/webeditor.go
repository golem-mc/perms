package core

import (
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golem-mc/golem/plugin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// BytebinClient communicates with LuckPerms's bytebin paste service (gzip POST / JSON GET).
type BytebinClient struct {
	BaseURL    string
	UserAgent  string
	HTTPClient *http.Client
}

// NewBytebinClient creates a BytebinClient for baseURL.
func NewBytebinClient(baseURL, userAgent string) *BytebinClient {
	return &BytebinClient{
		BaseURL:   ensureTrailingSlash(baseURL),
		UserAgent: userAgent,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// PostJSON gzip-compresses v as JSON and POSTs it to bytebin, returning the paste key.
func (b *BytebinClient) PostJSON(v any, uaExtra string) (string, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := json.NewEncoder(gz).Encode(v); err != nil {
		_ = gz.Close()
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	return b.PostGzip(buf.Bytes(), "application/json; charset=utf-8", uaExtra)
}

// PostGzip POSTs already gzip-compressed bytes to <BaseURL>post and returns the Location key.
func (b *BytebinClient) PostGzip(gzipped []byte, contentType, uaExtra string) (string, error) {
	req, err := http.NewRequest(http.MethodPost, b.BaseURL+"post", bytes.NewReader(gzipped))
	if err != nil {
		return "", err
	}
	ua := b.UserAgent
	if uaExtra != "" {
		ua += "/" + uaExtra
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := b.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("bytebin HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	key := resp.Header.Get("Location")
	if key == "" {
		var body struct {
			Key string `json:"key"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		key = body.Key
	}
	if key == "" {
		return "", errors.New("bytebin did not return a paste key")
	}
	return strings.TrimPrefix(key, "/"), nil
}

// GetJSON downloads and decodes JSON content for paste id.
func (b *BytebinClient) GetJSON(id string, dst any) error {
	req, err := http.NewRequest(http.MethodGet, b.BaseURL+id, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", b.UserAgent)
	resp, err := b.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("bytebin HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	var r io.Reader = resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	}
	return json.NewDecoder(r).Decode(dst)
}

// --- WebEditor Data Structures (matches LuckPerms WebEditorRequest / WebEditorResponse) ---

type WebEditorPayload struct {
	Metadata          WebEditorMetadata      `json:"metadata"`
	PermissionHolders []WebEditorHolder      `json:"permissionHolders"`
	Tracks            []WebEditorTrack       `json:"tracks"`
	KnownPermissions  []string               `json:"knownPermissions"`
	PotentialContexts map[string]any         `json:"potentialContexts"`
	Socket            *WebEditorSocketDetail `json:"socket,omitempty"`
}

type WebEditorMetadata struct {
	CommandAlias  string            `json:"commandAlias"`
	Uploader      WebEditorUploader `json:"uploader"`
	Time          int64             `json:"time"`
	PluginVersion string            `json:"pluginVersion"`
	Platform      string            `json:"platform"`
}

type WebEditorUploader struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
}

type WebEditorHolder struct {
	Type        string     `json:"type"`
	ID          string     `json:"id"`
	DisplayName string     `json:"displayName"`
	Nodes       []NodeJSON `json:"nodes"`
}

type WebEditorTrack struct {
	Type   string   `json:"type"`
	ID     string   `json:"id"`
	Groups []string `json:"groups"`
}

type WebEditorSocketDetail struct {
	ProtocolVersion int    `json:"protocolVersion"`
	ChannelID       string `json:"channelId"`
	PublicKey       string `json:"publicKey"`
}

type WebEditorResponsePayload struct {
	SessionID      string            `json:"sessionId"`
	Changes        []WebEditorChange `json:"changes,omitempty"`
	UserDeletions  []string          `json:"userDeletions,omitempty"`
	GroupDeletions []string          `json:"groupDeletions,omitempty"`
	TrackDeletions []string          `json:"trackDeletions,omitempty"`
}

type WebEditorChange struct {
	Type   string     `json:"type"`
	ID     string     `json:"id"`
	Nodes  []NodeJSON `json:"nodes,omitempty"`
	Groups []string   `json:"groups,omitempty"`
}

// RemoteEditorSession tracks an uploaded web editor request for diffing & replay detection.
type RemoteEditorSession struct {
	SessionID string
	Holders   map[string][]Node // key: "user:<uuid>" or "group:<name>"
	Tracks    map[string][]string
	Completed bool
	CreatedAt time.Time
}

// WebEditorStore manages active editor sessions, ECDSA P-256 keypair, trusted keys, and live sockets.
type WebEditorStore struct {
	lp      *LuckPerms
	keyPath string
	keyPair *ecdsa.PrivateKey
	pubDER  []byte

	mu       sync.RWMutex
	sessions map[string]*RemoteEditorSession
	trusted  map[string]map[string]bool // senderKey -> base64(pubKeyDER) -> true
	sockets  map[string]*WebEditorSocket
}

func newWebEditorStore(lp *LuckPerms) *WebEditorStore {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	var pubDER []byte
	if priv != nil {
		pubDER, _ = x509.MarshalPKIXPublicKey(&priv.PublicKey)
	}
	s := &WebEditorStore{
		lp:       lp,
		keyPath:  filepath.Join(lp.DataDir(), "editor-keystore.json"),
		keyPair:  priv,
		pubDER:   pubDER,
		sessions: make(map[string]*RemoteEditorSession),
		trusted:  make(map[string]map[string]bool),
		sockets:  make(map[string]*WebEditorSocket),
	}
	s.loadKeystore()
	return s
}

func (s *WebEditorStore) loadKeystore() {
	data, err := os.ReadFile(s.keyPath)
	if err != nil {
		return
	}
	var raw map[string][]string
	if json.Unmarshal(data, &raw) != nil {
		return
	}
	for k, keys := range raw {
		m := make(map[string]bool, len(keys))
		for _, pub := range keys {
			m[pub] = true
		}
		s.trusted[k] = m
	}
}

func (s *WebEditorStore) saveKeystoreLocked() {
	raw := make(map[string][]string, len(s.trusted))
	for k, m := range s.trusted {
		for pub := range m {
			raw[k] = append(raw[k], pub)
		}
	}
	if b, err := json.MarshalIndent(raw, "", "  "); err == nil {
		_ = atomicWriteFile(s.keyPath, b)
	}
}

func (s *WebEditorStore) IsTrusted(sender plugin.CommandSender, pubKeyB64 string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.trusted[verboseSenderKey(sender)][pubKeyB64]
}

func (s *WebEditorStore) Trust(sender plugin.CommandSender, pubKeyB64 string) {
	key := verboseSenderKey(sender)
	s.mu.Lock()
	if s.trusted[key] == nil {
		s.trusted[key] = make(map[string]bool)
	}
	s.trusted[key][pubKeyB64] = true
	s.saveKeystoreLocked()
	s.mu.Unlock()
}

func (s *WebEditorStore) PutSession(sess *RemoteEditorSession) {
	s.mu.Lock()
	s.sessions[sess.SessionID] = sess
	s.mu.Unlock()
}

func (s *WebEditorStore) GetSession(id string) *RemoteEditorSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

func (s *WebEditorStore) PutSocket(sender plugin.CommandSender, sock *WebEditorSocket) {
	key := verboseSenderKey(sender)
	s.mu.Lock()
	old := s.sockets[key]
	s.sockets[key] = sock
	s.mu.Unlock()
	if old != nil && old != sock {
		old.Close()
	}
}

func (s *WebEditorStore) GetSocket(sender plugin.CommandSender) *WebEditorSocket {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sockets[verboseSenderKey(sender)]
}

func (s *WebEditorStore) CloseAllSockets() {
	s.mu.Lock()
	socks := make([]*WebEditorSocket, 0, len(s.sockets))
	for _, sk := range s.sockets {
		socks = append(socks, sk)
	}
	clear(s.sockets)
	s.mu.Unlock()
	for _, sk := range socks {
		sk.Close()
	}
}

// --- ECDSA P-256 IEEE P1363 signature helpers (matches LuckPerms SignatureAlgorithm.V2_ECDSA) ---

func signP1363(priv *ecdsa.PrivateKey, msg string) (string, error) {
	sum := sha256.Sum256([]byte(msg))
	r, s, err := ecdsa.Sign(rand.Reader, priv, sum[:])
	if err != nil {
		return "", err
	}
	// P-256 curve order size is 32 bytes; P1363 encodes r || s (64 bytes total).
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return base64.StdEncoding.EncodeToString(sig), nil
}

func verifyP1363(pub *ecdsa.PublicKey, msg, sigB64 string) bool {
	raw, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(raw) != 64 {
		return false
	}
	r := new(big.Int).SetBytes(raw[:32])
	s := new(big.Int).SetBytes(raw[32:])
	sum := sha256.Sum256([]byte(msg))
	return ecdsa.Verify(pub, sum[:], r, s)
}

func parseECDSAPublicKey(b64 string) (*ecdsa.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	ecPub, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("public key is not ECDSA")
	}
	return ecPub, nil
}

// --- Live Bytesocks WebSocket session ---

type WebEditorSocket struct {
	lp       *LuckPerms
	sender   plugin.CommandSender
	cmdLabel string
	channel  string
	conn     *websocket.Conn
	holders  []string // "user:<uuid>" or "group:<name>"
	tracks   []string

	mu           sync.Mutex
	remotePub    *ecdsa.PublicKey
	remotePubB64 string
	attempted    map[string]string // nonce -> pubKeyB64
	lastPing     time.Time
	closed       bool
}

func (lp *LuckPerms) openBytesocksSocket(sender plugin.CommandSender, cmdLabel string, holders []string, tracks []string) (*WebEditorSocket, error) {
	base := ensureTrailingSlash(lp.Config().BytesocksURL)
	req, err := http.NewRequest(http.MethodGet, base+"create", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", lp.Bytebin.UserAgent)
	resp, err := lp.Bytebin.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("bytesocks create returned HTTP %d", resp.StatusCode)
	}
	channelID := strings.TrimPrefix(resp.Header.Get("Location"), "/")
	if channelID == "" {
		return nil, errors.New("bytesocks did not return channel Location")
	}

	wsURL := base + channelID
	if strings.HasPrefix(wsURL, "https://") {
		wsURL = "wss://" + strings.TrimPrefix(wsURL, "https://")
	} else if strings.HasPrefix(wsURL, "http://") {
		wsURL = "ws://" + strings.TrimPrefix(wsURL, "http://")
	}
	headers := http.Header{"User-Agent": {lp.Bytebin.UserAgent}}
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.Dial(wsURL, headers)
	if err != nil {
		return nil, err
	}

	sock := &WebEditorSocket{
		lp:        lp,
		sender:    sender,
		cmdLabel:  cmdLabel,
		channel:   channelID,
		conn:      conn,
		holders:   holders,
		tracks:    tracks,
		attempted: make(map[string]string),
		lastPing:  time.Now(),
	}
	lp.WebEditor.PutSocket(sender, sock)
	go sock.readLoop()
	return sock, nil
}

func (ws *WebEditorSocket) sendSigned(msg map[string]any) error {
	inner, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	sig, err := signP1363(ws.lp.WebEditor.keyPair, string(inner))
	if err != nil {
		return err
	}
	frame, _ := json.Marshal(map[string]string{
		"msg":       string(inner),
		"signature": sig,
	})
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.closed {
		return errors.New("socket closed")
	}
	return ws.conn.WriteMessage(websocket.TextMessage, frame)
}

func (ws *WebEditorSocket) readLoop() {
	defer ws.Close()
	for {
		_, data, err := ws.conn.ReadMessage()
		if err != nil {
			return
		}
		ws.handleFrame(data)
	}
}

func (ws *WebEditorSocket) handleFrame(data []byte) {
	var frame struct {
		Msg       string `json:"msg"`
		Signature string `json:"signature"`
	}
	if json.Unmarshal(data, &frame) != nil || frame.Msg == "" || frame.Signature == "" {
		return
	}
	var msg map[string]any
	if json.Unmarshal([]byte(frame.Msg), &msg) != nil {
		return
	}
	msgType := asString(msg["type"], "")

	if msgType == "hello" {
		ws.handleHello(msg)
		return
	}

	ws.mu.Lock()
	pub := ws.remotePub
	ws.mu.Unlock()
	if pub == nil || !verifyP1363(pub, frame.Msg, frame.Signature) {
		return
	}

	switch msgType {
	case "connected":
		SendInfo(ws.sender, "Editor window connected successfully.")
	case "ping":
		ws.mu.Lock()
		ws.lastPing = time.Now()
		ws.mu.Unlock()
		_ = ws.sendSigned(map[string]any{"type": "pong", "ok": true})
	case "change-request":
		code := asString(msg["code"], "")
		if code == "" {
			return
		}
		if !ws.lp.CanUseCommand(ws.sender, PermApplyEdits) {
			return
		}
		_ = ws.sendSigned(map[string]any{"type": "change-response", "state": "accepted"})
		SendInfo(ws.sender, "Changes have been received from the connected web editor session.")
		var payload WebEditorResponsePayload
		if err := ws.lp.Bytebin.GetJSON(code, &payload); err != nil {
			SendError(ws.sender, "Failed to download editor changes: "+err.Error())
			return
		}
		_ = ws.lp.ApplyWebEditorResponse(ws.sender, code, &payload, ws.cmdLabel, false)
		newCode, _ := ws.lp.CreateFollowUpWebEditorSession(ws.sender, ws.cmdLabel, ws.holders, ws.tracks)
		_ = ws.sendSigned(map[string]any{
			"type":           "change-response",
			"state":          "applied",
			"newSessionCode": newCode,
		})
	}
}

func (ws *WebEditorSocket) handleHello(msg map[string]any) {
	nonce := asString(msg["nonce"], "")
	sessionID := asString(msg["sessionId"], "")
	browser := asString(msg["browser"], "unknown")
	pubB64 := asString(msg["publicKey"], "")
	pub, err := parseECDSAPublicKey(pubB64)
	if err != nil || nonce == "" {
		return
	}

	ws.mu.Lock()
	if ws.remotePub != nil && ws.remotePubB64 != pubB64 {
		ws.mu.Unlock()
		_ = ws.sendSigned(map[string]any{"type": "hello-reply", "nonce": nonce, "state": "rejected"})
		return
	}
	sess := ws.lp.WebEditor.GetSession(sessionID)
	if sess == nil || sess.Completed {
		ws.mu.Unlock()
		_ = ws.sendSigned(map[string]any{"type": "hello-reply", "nonce": nonce, "state": "invalid"})
		return
	}
	if !ws.lp.WebEditor.IsTrusted(ws.sender, pubB64) {
		ws.attempted[nonce] = pubB64
		ws.mu.Unlock()
		_ = ws.sendSigned(map[string]any{"type": "hello-reply", "nonce": nonce, "state": "untrusted"})
		trustCmd := fmt.Sprintf("/%s trusteditor %s", ws.cmdLabel, nonce)
		SendPrefixed(ws.sender,
			plugin.Plain("An editor window has connected (").Color("gold"),
			plugin.Plain(browser).Color("aqua"),
			plugin.Plain("), but is not yet trusted. Run ").Color("gold"),
			plugin.Plain(trustCmd).Color("aqua").OnClick(plugin.RunCommand, trustCmd),
			plugin.Plain(" to trust it.").Color("gold"),
		)
		return
	}
	reconnected := ws.remotePub != nil
	ws.remotePub = pub
	ws.remotePubB64 = pubB64
	ws.mu.Unlock()

	_ = ws.sendSigned(map[string]any{"type": "hello-reply", "nonce": nonce, "state": "accepted"})
	if reconnected {
		SendInfo(ws.sender, "Editor window reconnected successfully.")
	} else {
		SendInfo(ws.sender, "Editor window connected successfully.")
	}
}

// TrustConnection marks the pending nonce's browser key as trusted and notifies the editor.
func (ws *WebEditorSocket) TrustConnection(nonce string) bool {
	ws.mu.Lock()
	if ws.closed || ws.remotePub != nil {
		ws.mu.Unlock()
		return false
	}
	pubB64, ok := ws.attempted[nonce]
	if !ok {
		ws.mu.Unlock()
		return false
	}
	pub, err := parseECDSAPublicKey(pubB64)
	if err != nil {
		ws.mu.Unlock()
		return false
	}
	ws.remotePub = pub
	ws.remotePubB64 = pubB64
	ws.mu.Unlock()

	ws.lp.WebEditor.Trust(ws.sender, pubB64)
	_ = ws.sendSigned(map[string]any{"type": "hello-reply", "nonce": nonce, "state": "trusted"})
	return true
}

// Close closes the live editor websocket.
func (ws *WebEditorSocket) Close() {
	ws.mu.Lock()
	if ws.closed {
		ws.mu.Unlock()
		return
	}
	ws.closed = true
	ws.mu.Unlock()
	_ = ws.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Normal"))
	_ = ws.conn.Close()
}

// --- High-level Web Editor session creation & edit application ---

// CreateWebEditorSession builds and uploads a LuckPerms web editor session to Bytebin
// (and optionally opens a live Bytesocks socket) returning (pasteID, editorURL, err).
func (lp *LuckPerms) CreateWebEditorSession(sender plugin.CommandSender, cmdLabel string, groups []*Group, users []*User, tracks []*Track, withSocket bool) (string, string, error) {
	if len(groups) == 0 && len(users) == 0 && len(tracks) == 0 {
		return "", "", errors.New("no permission holders matched")
	}

	payload, sessHolders, sessTracks, holderIDs, trackIDs := lp.buildWebEditorPayload(sender, cmdLabel, groups, users, tracks)

	if withSocket && lp.WebEditor.keyPair != nil {
		if sock, err := lp.openBytesocksSocket(sender, cmdLabel, holderIDs, trackIDs); err == nil {
			payload.Socket = &WebEditorSocketDetail{
				ProtocolVersion: 2,
				ChannelID:       sock.channel,
				PublicKey:       base64.StdEncoding.EncodeToString(lp.WebEditor.pubDER),
			}
		}
	}

	pasteID, err := lp.Bytebin.PostJSON(payload, "editor")
	if err != nil {
		return "", "", err
	}
	lp.WebEditor.PutSession(&RemoteEditorSession{
		SessionID: pasteID,
		Holders:   sessHolders,
		Tracks:    sessTracks,
		CreatedAt: time.Now(),
	})
	url := ensureTrailingSlash(lp.Config().WebEditorURL) + pasteID
	return pasteID, url, nil
}

// CreateFollowUpWebEditorSession uploads a refreshed snapshot after live socket edits.
func (lp *LuckPerms) CreateFollowUpWebEditorSession(sender plugin.CommandSender, cmdLabel string, holderIDs, trackIDs []string) (string, error) {
	var groups []*Group
	var users []*User
	var tracks []*Track
	for _, hid := range holderIDs {
		if gname, ok := strings.CutPrefix(hid, "group:"); ok {
			if g := lp.Groups.GetIfLoaded(gname); g != nil {
				groups = append(groups, g)
			}
		} else if uidStr, ok := strings.CutPrefix(hid, "user:"); ok {
			if uid, err := uuid.Parse(uidStr); err == nil {
				if u, err := lp.Storage.LoadUser(uid, ""); err == nil {
					users = append(users, u)
				}
			}
		}
	}
	for _, tid := range trackIDs {
		if t := lp.Tracks.GetIfLoaded(tid); t != nil {
			tracks = append(tracks, t)
		}
	}
	pasteID, _, err := lp.CreateWebEditorSession(sender, cmdLabel, groups, users, tracks, false)
	return pasteID, err
}

func (lp *LuckPerms) buildWebEditorPayload(sender plugin.CommandSender, cmdLabel string, groups []*Group, users []*User, tracks []*Track) (
	payload WebEditorPayload,
	sessHolders map[string][]Node,
	sessTracks map[string][]string,
	holderIDs []string,
	trackIDs []string,
) {
	srcUUID := ConsoleUUID
	srcName := "Console"
	if sender != nil {
		srcName = sender.Name()
		if p, ok := sender.(plugin.Player); ok {
			srcUUID = p.UUID()
		}
	}

	potCtx := lp.Contexts.PotentialContexts().MutableCopy()
	sessHolders = make(map[string][]Node)
	sessTracks = make(map[string][]string)

	var holdersJSON []WebEditorHolder
	for _, g := range groups {
		nodes := g.NormalNodes()
		jn := make([]NodeJSON, len(nodes))
		for i, n := range nodes {
			jn[i] = n.ToJSON()
			potCtx.AddAll(n.Contexts)
		}
		holdersJSON = append(holdersJSON, WebEditorHolder{
			Type:        "group",
			ID:          g.Name(),
			DisplayName: g.FriendlyDisplayName(lp.Contexts.StaticQueryOptions()),
			Nodes:       jn,
		})
		key := "group:" + g.Name()
		sessHolders[key] = nodes
		holderIDs = append(holderIDs, key)
	}
	for _, u := range users {
		nodes := u.NormalNodes()
		jn := make([]NodeJSON, len(nodes))
		for i, n := range nodes {
			jn[i] = n.ToJSON()
			potCtx.AddAll(n.Contexts)
		}
		holdersJSON = append(holdersJSON, WebEditorHolder{
			Type:        "user",
			ID:          u.UUID().String(),
			DisplayName: u.FriendlyName(),
			Nodes:       jn,
		})
		key := "user:" + u.UUID().String()
		sessHolders[key] = nodes
		holderIDs = append(holderIDs, key)
	}

	tracksJSON := make([]WebEditorTrack, 0, len(tracks))
	for _, t := range tracks {
		gs := t.Groups()
		if gs == nil {
			gs = []string{}
		}
		tracksJSON = append(tracksJSON, WebEditorTrack{
			Type:   "track",
			ID:     t.Name(),
			Groups: gs,
		})
		sessTracks[t.Name()] = gs
		trackIDs = append(trackIDs, t.Name())
	}

	ctxMap := ContextSetToJSONMap(potCtx.Immutable())
	if ctxMap == nil {
		ctxMap = map[string]any{}
	}

	payload = WebEditorPayload{
		Metadata: WebEditorMetadata{
			CommandAlias:  cmdLabel,
			Uploader:      WebEditorUploader{Name: srcName, UUID: srcUUID.String()},
			Time:          time.Now().UnixMilli(),
			PluginVersion: Version,
			Platform:      "golem",
		},
		PermissionHolders: holdersJSON,
		Tracks:            tracksJSON,
		KnownPermissions:  lp.Registry.All(),
		PotentialContexts: ctxMap,
	}
	return
}

// ApplyWebEditorResponse applies a downloaded WebEditorResponsePayload to LuckPerms storage.
func (lp *LuckPerms) ApplyWebEditorResponse(sender plugin.CommandSender, code string, resp *WebEditorResponsePayload, cmdLabel string, force bool) error {
	sess := lp.WebEditor.GetSession(resp.SessionID)
	if !force {
		if sess == nil {
			return fmt.Errorf("unknown session %q; re-run '/%s applyedits %s --force' to apply anyway", resp.SessionID, cmdLabel, code)
		}
		if sess.Completed {
			return fmt.Errorf("session %q has already been applied; re-run '/%s applyedits %s --force' to apply anyway", resp.SessionID, cmdLabel, code)
		}
	}

	work := false
	for _, ch := range resp.Changes {
		switch strings.ToLower(ch.Type) {
		case "group":
			name := strings.ToLower(ch.ID)
			g := lp.Groups.GetIfLoaded(name)
			if g == nil {
				var err error
				g, err = lp.Storage.CreateAndLoadGroup(name)
				if err != nil {
					SendError(sender, "Failed to create group "+name+": "+err.Error())
					continue
				}
				lp.FireGroupCreate(g)
			}
			var incoming []Node
			for _, jn := range ch.Nodes {
				if n, ok := NodeFromJSON(jn); ok {
					incoming = append(incoming, n)
				}
			}
			added, removed := applyEditorNodeDiff(&g.PermissionHolder, sess, "group:"+name, incoming)
			if len(added) == 0 && len(removed) == 0 {
				continue
			}
			work = true
			_ = lp.Storage.SaveGroup(g)
			for _, n := range added {
				lp.Actions.Submit(sender, TargetGroup, nil, name, fmt.Sprintf("webeditor add %s %v", n.Key, n.Value))
			}
			for _, n := range removed {
				lp.Actions.Submit(sender, TargetGroup, nil, name, fmt.Sprintf("webeditor remove %s %v", n.Key, n.Value))
			}
			SendInfo(sender, fmt.Sprintf("Applied web editor changes to group %s (+%d additions, -%d deletions).", name, len(added), len(removed)))

		case "user":
			uid, err := uuid.Parse(ch.ID)
			if err != nil {
				SendError(sender, "Invalid user UUID in editor changes: "+ch.ID)
				continue
			}
			var incoming []Node
			for _, jn := range ch.Nodes {
				if n, ok := NodeFromJSON(jn); ok {
					incoming = append(incoming, n)
				}
			}
			_ = lp.WithUser(uid, "", false, func(u *User) error {
				added, removed := applyEditorNodeDiff(&u.PermissionHolder, sess, "user:"+uid.String(), incoming)
				if len(added) == 0 && len(removed) == 0 {
					return nil
				}
				work = true
				u.GiveDefaultIfNeeded()
				_ = lp.Storage.SaveUser(u)
				uCopy := uid
				for _, n := range added {
					lp.Actions.Submit(sender, TargetUser, &uCopy, u.FriendlyName(), fmt.Sprintf("webeditor add %s %v", n.Key, n.Value))
				}
				for _, n := range removed {
					lp.Actions.Submit(sender, TargetUser, &uCopy, u.FriendlyName(), fmt.Sprintf("webeditor remove %s %v", n.Key, n.Value))
				}
				SendInfo(sender, fmt.Sprintf("Applied web editor changes to user %s (+%d additions, -%d deletions).", u.FriendlyName(), len(added), len(removed)))
				lp.PushUserUpdateAsync(u)
				return nil
			})

		case "track":
			name := strings.ToLower(ch.ID)
			t := lp.Tracks.GetIfLoaded(name)
			if t == nil {
				var err error
				t, err = lp.Storage.CreateAndLoadTrack(name)
				if err != nil {
					SendError(sender, "Failed to create track "+name+": "+err.Error())
					continue
				}
			}
			before := t.Groups()
			if slices.Equal(before, ch.Groups) {
				continue
			}
			work = true
			t.SetGroups(ch.Groups)
			_ = lp.Storage.SaveTrack(t)
			lp.Actions.Submit(sender, TargetTrack, nil, name, fmt.Sprintf("webeditor update %v", ch.Groups))
			SendInfo(sender, fmt.Sprintf("Applied web editor changes to track %s (%v -> %v).", name, before, ch.Groups))
		}
	}

	for _, uidStr := range resp.UserDeletions {
		uid, err := uuid.Parse(uidStr)
		if err != nil {
			continue
		}
		_ = lp.WithUser(uid, "", false, func(u *User) error {
			u.ClearNodes(DataNormal, func(Node) bool { return true })
			u.SetStoredPrimaryGroup(DefaultGroupName)
			_ = lp.Storage.SaveUser(u)
			_ = lp.Storage.DeletePlayerData(uid)
			uCopy := uid
			lp.Actions.Submit(sender, TargetUser, &uCopy, u.FriendlyName(), "webeditor delete")
			SendInfo(sender, "Deleted user data for "+u.FriendlyName()+".")
			work = true
			return nil
		})
	}

	for _, gName := range resp.GroupDeletions {
		key := strings.ToLower(gName)
		if key == DefaultGroupName {
			SendError(sender, "Cannot delete the default group.")
			continue
		}
		if g := lp.Groups.GetIfLoaded(key); g != nil {
			_ = lp.Storage.DeleteGroup(g)
			lp.FireGroupDelete(key)
			lp.Actions.Submit(sender, TargetGroup, nil, key, "webeditor delete")
			SendInfo(sender, "Deleted group "+key+".")
			work = true
		}
	}

	for _, tName := range resp.TrackDeletions {
		key := strings.ToLower(tName)
		if t := lp.Tracks.GetIfLoaded(key); t != nil {
			_ = lp.Storage.DeleteTrack(t)
			lp.Actions.Submit(sender, TargetTrack, nil, key, "webeditor delete")
			SendInfo(sender, "Deleted track "+key+".")
			work = true
		}
	}

	if sess != nil {
		sess.Completed = true
	}
	if !work {
		SendInfo(sender, "No changes were present in the editor session.")
	} else {
		lp.InvalidateAllCaches()
		lp.PushUpdateAsync()
	}
	return nil
}

// applyEditorNodeDiff computes exact additions/deletions, using the initial session snapshot when available
// so concurrent edits made while the editor was open are preserved (matching Java WebEditorResponse).
func applyEditorNodeDiff(h *PermissionHolder, sess *RemoteEditorSession, sessKey string, incoming []Node) (added, removed []Node) {
	if sess != nil {
		if before, ok := sess.Holders[sessKey]; ok {
			for _, n := range incoming {
				if !containsExactNode(before, n) {
					added = append(added, n)
				}
			}
			for _, n := range before {
				if !containsExactNode(incoming, n) {
					removed = append(removed, n)
				}
			}
			for _, n := range removed {
				h.UnsetNodeMatching(DataNormal, n, EqualityExact)
			}
			for _, n := range added {
				h.SetNode(DataNormal, n)
			}
			return added, removed
		}
	}
	before := h.NormalNodes()
	for _, n := range incoming {
		if !containsExactNode(before, n) {
			added = append(added, n)
		}
	}
	for _, n := range before {
		if !containsExactNode(incoming, n) {
			removed = append(removed, n)
		}
	}
	h.Data(DataNormal).SetContent(incoming)
	h.InvalidateCache()
	return added, removed
}

func containsExactNode(list []Node, target Node) bool {
	for _, n := range list {
		if n.Equal(target, EqualityExact) {
			return true
		}
	}
	return false
}

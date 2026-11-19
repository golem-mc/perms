package core

import (
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestNodeParsingAndDelimiters(t *testing.T) {
	n := ParseNode("group.Admin", true)
	if n.Type != NodeTypeInheritance || n.GroupName != "admin" || n.Key != "group.admin" {
		t.Fatalf("unexpected inheritance node: %+v", n)
	}

	p := NewPrefixNode(100, "&c[Admin.] ")
	if p.Key != `prefix.100.&c[Admin\.] ` {
		t.Fatalf("unexpected prefix key: %q", p.Key)
	}
	p2 := ParseNode(p.Key, true)
	if p2.Type != NodeTypePrefix || p2.Priority != 100 || p2.MetaValue != "&c[Admin.] " {
		t.Fatalf("prefix round-trip failed: %+v", p2)
	}

	s := NewSuffixNode(50, " &7(v1.2)")
	s2 := ParseNode(s.Key, true)
	if s2.Type != NodeTypeSuffix || s2.Priority != 50 || s2.MetaValue != " &7(v1.2)" {
		t.Fatalf("suffix round-trip failed: %+v", s2)
	}

	m := NewMetaNode("home.limit", "10.5")
	m2 := ParseNode(m.Key, true)
	if m2.Type != NodeTypeMeta || m2.MetaKey != "home.limit" || m2.MetaValue != "10.5" {
		t.Fatalf("meta round-trip failed: %+v", m2)
	}

	w := ParseNode("weight.42", true)
	if w.Type != NodeTypeWeight || w.Weight != 42 {
		t.Fatalf("weight parse failed: %+v", w)
	}

	dn := ParseNode("displayname.Staff", true)
	if dn.Type != NodeTypeDisplayName || dn.DisplayName != "Staff" {
		t.Fatalf("displayname parse failed: %+v", dn)
	}

	rx := ParseNode(`r=kit\.(starter|vip)`, true)
	if rx.Type != NodeTypeRegexPermission || rx.Pattern != `kit\.(starter|vip)` {
		t.Fatalf("regex parse failed: %+v", rx)
	}

	if got := UnescapeDelimiters(`a\.b\/c\-d\$e`); got != "a.b/c-d$e" {
		t.Fatalf("legacy unescape failed: %q", got)
	}
}

func TestShorthandExpansion(t *testing.T) {
	got, err := ExpandShorthand("perm.{1-3}.{a,b}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"perm.1.a", "perm.1.b", "perm.2.a", "perm.2.b", "perm.3.a", "perm.3.b"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	gotChar, err := ExpandShorthand("test.(a-c)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantChar := []string{"test.a", "test.b", "test.c"}
	if !slices.Equal(gotChar, wantChar) {
		t.Fatalf("got %v, want %v", gotChar, wantChar)
	}

	if _, err := ExpandShorthand("big.{1-500}"); err == nil {
		t.Fatal("expected range limit error")
	}
}

func TestDurationParsing(t *testing.T) {
	d, err := ParseDuration("1d2h30m15s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := 24*time.Hour + 2*time.Hour + 30*time.Minute + 15*time.Second
	if d != want {
		t.Fatalf("got %v, want %v", d, want)
	}
	if s := FormatDuration(d); s != "1d 2h 30m 15s" {
		t.Fatalf("unexpected formatted duration: %q", s)
	}

	if _, err := ParseDuration("1000"); err != ErrPastDate {
		t.Fatalf("expected ErrPastDate for past unix ts, got %v", err)
	}
}

func TestContextSatisfaction(t *testing.T) {
	req := NewContextSet(
		Context{Key: "world", Value: "world"},
		Context{Key: "world", Value: "world_nether"},
		Context{Key: "server", Value: "survival"},
	)
	active := NewContextSet(
		Context{Key: "world", Value: "world_nether"},
		Context{Key: "server", Value: "survival"},
	)
	if !req.IsSatisfiedBy(active, AtLeastOneValuePerKey) {
		t.Fatal("expected AtLeastOneValuePerKey to match")
	}
	if req.IsSatisfiedBy(active, AllValuesPerKey) {
		t.Fatal("expected AllValuesPerKey not to match")
	}

	// Global server/world values are stripped.
	g := NewContextSet(Context{Key: "server", Value: "global"}, Context{Key: "world", Value: "global"})
	if !g.IsEmpty() {
		t.Fatalf("expected global server/world to be stripped, got %v", g)
	}

	// Recursive world rewrites with cycle protection.
	rewrites := map[string]string{"world_nether": "nether_group", "nether_group": "all_worlds", "all_worlds": "world_nether"}
	m := NewMutableContextSet()
	rewriteWorld("world_nether", rewrites, m.Add)
	got := m.Immutable()
	for _, w := range []string{"world_nether", "nether_group", "all_worlds"} {
		if !got.Contains("world", w) {
			t.Fatalf("missing rewritten world %q in %v", w, got)
		}
	}
}

func newUnitLuckPerms(t *testing.T, storageMethod string) *LuckPerms {
	t.Helper()
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.StorageMethod = storageMethod
	lp := &LuckPerms{
		StartedAt:  time.Now(),
		config:     cfg,
		Registry:   newPermissionRegistry(),
		Verbose:    newVerboseHandler(),
		watchState: make(map[string]time.Time),
	}
	lp.Users = newUserManager(lp)
	lp.Groups = newGroupManager(lp)
	lp.Tracks = newTrackManager(lp)
	lp.Contexts = newContextManager(lp)
	lp.Actions = newActionLogger(lp)
	lp.Bytebin = NewBytebinClient(cfg.BytebinURL, "luckperms-test")
	lp.WebEditor = newWebEditorStore(lp)
	lp.Translations = newTranslationManager(lp)

	switch storageMethod {
	case "sqlite":
		sq := NewSQLStorage(lp, DialectSQLite)
		sq.dbPath = filepath.Join(dir, "luckperms-sqlite.db")
		lp.Storage = sq
	case "split":
		sq := NewSQLStorage(lp, DialectSQLite)
		sq.dbPath = filepath.Join(dir, "split-sqlite.db")
		yf := NewFileStorage(lp, FormatYAML, false)
		yf.dir = filepath.Join(dir, "split-yaml")
		tf := NewFileStorage(lp, FormatTOML, false)
		tf.dir = filepath.Join(dir, "split-toml")
		jf := NewFileStorage(lp, FormatJSON, false)
		jf.dir = filepath.Join(dir, "split-json")
		lp.Storage = &SplitStorage{
			backends: map[string]Storage{"sqlite": sq, "yaml": yf, "toml": tf, "json": jf},
			user:     sq,
			group:    yf,
			track:    tf,
			uuid:     jf,
			log:      sq,
		}
	default:
		format := FormatYAML
		combined := false
		switch storageMethod {
		case "yaml-combined":
			combined = true
		case "json":
			format = FormatJSON
		case "json-combined":
			format = FormatJSON
			combined = true
		case "toml":
			format = FormatTOML
		case "toml-combined":
			format = FormatTOML
			combined = true
		}
		fs := NewFileStorage(lp, format, combined)
		fs.dir = filepath.Join(dir, storageMethod)
		lp.Storage = fs
	}
	if err := lp.Storage.Init(); err != nil {
		t.Fatalf("storage init: %v", err)
	}
	t.Cleanup(func() { _ = lp.Storage.Shutdown() })
	if err := lp.Storage.LoadAllGroups(); err != nil {
		t.Fatalf("load groups: %v", err)
	}
	return lp
}

func TestCalculatorWildcardRegexShorthandAndInheritance(t *testing.T) {
	lp := newUnitLuckPerms(t, "yaml")

	def := lp.Groups.GetOrMake("default")
	def.SetNode(DataNormal, NewPermissionNode("base.chat", true))
	def.SetNode(DataNormal, NewWeightNode(10))
	def.SetNode(DataNormal, NewPrefixNode(10, "[Member] "))
	def.SetNode(DataNormal, NewMetaNode("homes", "3"))

	vip := lp.Groups.GetOrMake("vip")
	vip.SetNode(DataNormal, NewInheritanceNode("default"))
	vip.SetNode(DataNormal, NewWeightNode(50))
	vip.SetNode(DataNormal, NewPermissionNode("essentials.*", true))
	vip.SetNode(DataNormal, NewPermissionNode("essentials.ban", false))
	vip.SetNode(DataNormal, NewRegexNode(`kit\.(starter|vip)`, true))
	vip.SetNode(DataNormal, NewPermissionNode("plot.{1-3}", true))
	vip.SetNode(DataNormal, NewPrefixNode(50, "[VIP] "))
	vip.SetNode(DataNormal, NewMetaNode("homes", "10"))

	id := uuid.New()
	u := lp.Users.GetOrMake(id, "Alice")
	u.SetNode(DataNormal, NewInheritanceNode("vip"))
	u.SetNode(DataNormal, NewPermissionNode("essentials.tpa", false))

	qo := lp.Contexts.StaticQueryOptions()
	pd := u.CachedData().PermissionData(qo)

	cases := []struct {
		perm string
		want Tristate
	}{
		{"base.chat", TristateTrue},
		{"essentials.home", TristateTrue},
		{"essentials.ban", TristateFalse},
		{"essentials.tpa", TristateFalse},
		{"kit.vip", TristateTrue},
		{"kit.admin", TristateUndefined},
		{"plot.2", TristateTrue},
		{"plot.4", TristateUndefined},
	}
	for _, tc := range cases {
		if got := pd.CheckPermission(tc.perm, OriginInternal).Result; got != tc.want {
			t.Errorf("perm %q: got %v, want %v", tc.perm, got, tc.want)
		}
	}

	// Meta & Prefix stacking
	md := u.CachedData().MetaData(qo)
	if got := md.Prefix(); got != "[VIP] " {
		t.Fatalf("prefix: got %q, want %q", got, "[VIP] ")
	}
	if pg := u.PrimaryGroup(qo); pg != "vip" {
		t.Fatalf("primary group: got %q, want %q", pg, "vip")
	}
	if homes, ok := md.MetaValue("homes"); !ok || homes != "10" {
		t.Fatalf("meta homes: got %q (%v), want 10", homes, ok)
	}

	// Test lowest-number selection
	lp.Config().MetaValueSelectionPerKey["homes"] = MetaSelectLowestNumber
	if homes, ok := md.MetaValue("homes"); !ok || homes != "3" {
		t.Fatalf("meta homes lowest-number: got %q (%v), want 3", homes, ok)
	}

	// Test prefix stacking (highest + lowest)
	lp.Config().PrefixFormatting = MetaStackConfig{
		Format:       []string{"highest", "lowest"},
		Duplicates:   DuplicateFirstOnly,
		StartSpacer:  "",
		MiddleSpacer: "|",
		EndSpacer:    "",
	}
	u.InvalidateCache()
	md2 := u.CachedData().MetaData(qo)
	if got := md2.Prefix(); got != "[VIP] |[Member] " {
		t.Fatalf("stacked prefix: got %q, want %q", got, "[VIP] |[Member] ")
	}
}

func TestAllStorageBackendsRoundTrip(t *testing.T) {
	for _, method := range []string{"yaml", "yaml-combined", "json", "json-combined", "toml", "toml-combined", "sqlite", "split"} {
		t.Run(method, func(t *testing.T) {
			lp := newUnitLuckPerms(t, method)

			g, err := lp.Storage.CreateAndLoadGroup("admin")
			if err != nil {
				t.Fatalf("create group: %v", err)
			}
			exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
			g.SetNode(DataNormal, NewPermissionNode("example.perm", true))
			g.SetNode(DataNormal, NewPermissionNode("example.deny", false).WithContexts(NewContextSet(Context{"world", "world_nether"})))
			g.SetNode(DataNormal, NewPrefixNode(100, "&c[Admin] "))
			g.SetNode(DataNormal, NewSuffixNode(20, " &7*"))
			g.SetNode(DataNormal, NewMetaNode("rank", "high").WithExpiry(exp))
			g.SetNode(DataNormal, NewWeightNode(100))
			if err := lp.Storage.SaveGroup(g); err != nil {
				t.Fatalf("save group: %v", err)
			}

			tr, err := lp.Storage.CreateAndLoadTrack("staff")
			if err != nil {
				t.Fatalf("create track: %v", err)
			}
			tr.AppendGroup("default")
			tr.AppendGroup("admin")
			if err := lp.Storage.SaveTrack(tr); err != nil {
				t.Fatalf("save track: %v", err)
			}

			uid := uuid.New()
			u := lp.Users.GetOrMake(uid, "Bob")
			u.SetNode(DataNormal, NewInheritanceNode("admin"))
			u.SetStoredPrimaryGroup("admin")
			if err := lp.Storage.SaveUser(u); err != nil {
				t.Fatalf("save user: %v", err)
			}
			if err := lp.Storage.SavePlayerData(uid, "Bob"); err != nil {
				t.Fatalf("save player data: %v", err)
			}

			// Reload fresh from disk
			lp.Groups.Unload("admin")
			lp.Tracks.Unload("staff")
			lp.Users.Unload(uid)

			if err := lp.Storage.LoadAllGroups(); err != nil {
				t.Fatalf("reload groups: %v", err)
			}
			if err := lp.Storage.LoadAllTracks(); err != nil {
				t.Fatalf("reload tracks: %v", err)
			}
			g2 := lp.Groups.GetIfLoaded("admin")
			if g2 == nil {
				t.Fatal("admin group missing after reload")
			}
			if w, ok := g2.Weight(); !ok || w != 100 {
				t.Fatalf("weight after reload: got %d (%v), want 100", w, ok)
			}
			md := g2.CachedData().MetaData(lp.Contexts.StaticQueryOptions())
			if md.Prefix() != "&c[Admin] " || md.Suffix() != " &7*" {
				t.Fatalf("chat meta mismatch: prefix=%q suffix=%q", md.Prefix(), md.Suffix())
			}
			if v, ok := md.MetaValue("rank"); !ok || v != "high" {
				t.Fatalf("meta rank mismatch: %q (%v)", v, ok)
			}

			tr2 := lp.Tracks.GetIfLoaded("staff")
			if tr2 == nil || !slices.Equal(tr2.Groups(), []string{"default", "admin"}) {
				t.Fatalf("track mismatch after reload: %+v", tr2)
			}

			u2, err := lp.Storage.LoadUser(uid, "")
			if err != nil {
				t.Fatalf("reload user: %v", err)
			}
			if !strings.EqualFold(u2.Username(), "Bob") || u2.StoredPrimaryGroup() != "admin" {
				t.Fatalf("user metadata mismatch: name=%q pg=%q", u2.Username(), u2.StoredPrimaryGroup())
			}
			if gotID, ok := lp.Storage.GetPlayerUniqueID("bob"); !ok || gotID != uid {
				t.Fatalf("uuid cache lookup failed: %v (%v)", gotID, ok)
			}
		})
	}
}

func TestVerboseFilterAndBulkUpdate(t *testing.T) {
	f, err := CompileVerboseFilter("(Alice & !minecraft.) | essentials.home")
	if err != nil {
		t.Fatalf("compile filter: %v", err)
	}
	if !f.Matches(VerboseEvent{Target: "Alice", Permission: "custom.perm"}) {
		t.Fatal("expected Alice custom.perm to match")
	}
	if f.Matches(VerboseEvent{Target: "Alice", Permission: "minecraft.command.gamemode"}) {
		t.Fatal("expected Alice minecraft.* to be excluded")
	}
	if !f.Matches(VerboseEvent{Target: "Bob", Permission: "essentials.home"}) {
		t.Fatal("expected Bob essentials.home to match")
	}

	bu := &BulkUpdate{
		DataType:        BulkDataGroups,
		Action:          BulkActionUpdate,
		UpdateField:     BulkFieldWorld,
		UpdateValue:     "survival",
		TrackStatistics: true,
		Constraints: []BulkConstraint{
			{Field: BulkFieldPermission, Comparison: BulkLike, Value: "essentials.%"},
		},
	}
	nodes := []Node{
		NewPermissionNode("essentials.home", true),
		NewPermissionNode("other.perm", true),
	}
	updated, changed := bu.Apply(nodes, HolderGroup)
	if !changed || bu.Stats.AffectedNodes != 1 || bu.Stats.AffectedGroups != 1 {
		t.Fatalf("unexpected bulkupdate stats: changed=%v stats=%+v", changed, bu.Stats)
	}
	if !updated[0].Contexts.Contains("world", "survival") || !updated[1].Contexts.IsEmpty() {
		t.Fatalf("unexpected updated nodes: %+v", updated)
	}
}

func TestReadJavaLuckPermsYAMLFormat(t *testing.T) {
	lp := newUnitLuckPerms(t, "yaml")
	// Exact format written by Java LuckPerms YamlLoader
	javaYAML := `name: moderator
permissions:
- essentials.kick
- essentials.ban:
    value: false
    context:
      world: world_nether
parents:
- default
prefixes:
- '&b[Mod] ':
    priority: 80
meta:
- chat-color:
    value: aqua
`
	path := filepath.Join(lp.Storage.DataDir(), "groups", "moderator.yml")
	if err := os.WriteFile(path, []byte(javaYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := lp.Storage.LoadAllGroups(); err != nil {
		t.Fatal(err)
	}
	mod := lp.Groups.GetIfLoaded("moderator")
	if mod == nil {
		t.Fatal("moderator group not loaded")
	}
	qoOverworld := lp.Contexts.StaticQueryOptions()
	if !mod.CachedData().PermissionData(qoOverworld).CheckPermission("essentials.kick", OriginInternal).Result.AsBool() {
		t.Fatal("expected essentials.kick=true")
	}
	qoNether := lp.defaultQueryOptions(NewContextSet(Context{"world", "world_nether"}))
	if mod.CachedData().PermissionData(qoNether).CheckPermission("essentials.ban", OriginInternal).Result != TristateFalse {
		t.Fatal("expected essentials.ban=false in world_nether")
	}
	md := mod.CachedData().MetaData(qoOverworld)
	if md.Prefix() != "&b[Mod] " {
		t.Fatalf("prefix: got %q", md.Prefix())
	}
	if v, ok := md.MetaValue("chat-color"); !ok || v != "aqua" {
		t.Fatalf("meta chat-color: got %q (%v)", v, ok)
	}
}

func TestSQLMessengerAndCustomPayload(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "shared.db")

	makeNode := func() *LuckPerms {
		lp := newUnitLuckPerms(t, "yaml")
		_ = lp.Storage.Shutdown()
		sq := NewSQLStorage(lp, DialectSQLite)
		sq.dbPath = dbPath
		lp.Storage = sq
		if err := sq.Init(); err != nil {
			t.Fatal(err)
		}
		if err := sq.LoadAllGroups(); err != nil {
			t.Fatal(err)
		}
		return lp
	}

	lp1 := makeNode()
	lp2 := makeNode()

	ms1, err := NewMessagingService(lp1, &sqlMessengerProvider{lp: lp1, sql: lp1.Storage.(*SQLStorage)})
	if err != nil {
		t.Fatalf("ms1: %v", err)
	}
	defer ms1.Close()
	ms2, err := NewMessagingService(lp2, &sqlMessengerProvider{lp: lp2, sql: lp2.Storage.(*SQLStorage)})
	if err != nil {
		t.Fatalf("ms2: %v", err)
	}
	defer ms2.Close()

	gotChan := make(chan string, 1)
	ms2.OnCustomMessage(func(ch, payload string) {
		gotChan <- ch + ":" + payload
	})

	if err := ms1.PushCustomPayload("test:ch", "hello-network"); err != nil {
		t.Fatalf("push custom: %v", err)
	}
	// Trigger poll on ms2
	ms2.messenger.(*sqlMessenger).pollMessages()

	select {
	case got := <-gotChan:
		if got != "test:ch:hello-network" {
			t.Fatalf("got %q, want test:ch:hello-network", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SQL messenger delivery")
	}
}

func TestWebEditorBytebinAndLiveSocket(t *testing.T) {
	var mu sync.Mutex
	pastes := map[string][]byte{}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	serverConnCh := make(chan *websocket.Conn, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/post":
			var reader io.Reader = r.Body
			if r.Header.Get("Content-Encoding") == "gzip" {
				gz, err := gzip.NewReader(r.Body)
				if err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				defer gz.Close()
				reader = gz
			}
			body, _ := io.ReadAll(reader)
			mu.Lock()
			key := "paste" + string(rune('1'+len(pastes)))
			pastes[key] = body
			mu.Unlock()
			w.Header().Set("Location", key)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/create":
			w.Header().Set("Location", "chan42")
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/chan42":
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			serverConnCh <- c
			// Keep connection open until test closes server
			select {}
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/"):
			key := strings.TrimPrefix(r.URL.Path, "/")
			mu.Lock()
			body, ok := pastes[key]
			mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		}
	}))
	defer srv.Close()

	lp := newUnitLuckPerms(t, "yaml")
	lp.Config().BytebinURL = srv.URL + "/"
	lp.Config().BytesocksURL = srv.URL + "/"
	lp.Bytebin = NewBytebinClient(srv.URL+"/", "lp-test")
	lp.WebEditor.keyPath = filepath.Join(t.TempDir(), "keystore.json")

	g, _ := lp.Storage.CreateAndLoadGroup("vip")
	g.SetNode(DataNormal, NewPermissionNode("existing.node", true))
	_ = lp.Storage.SaveGroup(g)

	// Open session with live socket.
	pasteID, url, err := lp.CreateWebEditorSession(nil, "lp", lp.Groups.All(), nil, nil, true)
	if err != nil {
		t.Fatalf("CreateWebEditorSession: %v", err)
	}
	if !strings.HasSuffix(url, pasteID) {
		t.Fatalf("unexpected editor url: %q", url)
	}

	// Simulate remote browser editor connecting over Bytesocks.
	wsSrv := <-serverConnCh
	defer wsSrv.Close()

	clientKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	clientPubDER, _ := x509.MarshalPKIXPublicKey(&clientKey.PublicKey)
	clientPubB64 := base64.StdEncoding.EncodeToString(clientPubDER)

	sendClientMsg := func(msg map[string]any) {
		inner, _ := json.Marshal(msg)
		sig, _ := signP1363(clientKey, string(inner))
		frame, _ := json.Marshal(map[string]string{"msg": string(inner), "signature": sig})
		if err := wsSrv.WriteMessage(websocket.TextMessage, frame); err != nil {
			t.Fatalf("write ws frame: %v", err)
		}
	}
	readPluginMsg := func() map[string]any {
		_ = wsSrv.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, raw, err := wsSrv.ReadMessage()
		if err != nil {
			t.Fatalf("read ws frame: %v", err)
		}
		var frame struct {
			Msg       string `json:"msg"`
			Signature string `json:"signature"`
		}
		_ = json.Unmarshal(raw, &frame)
		if !verifyP1363(&lp.WebEditor.keyPair.PublicKey, frame.Msg, frame.Signature) {
			t.Fatal("plugin signature verification failed")
		}
		var m map[string]any
		_ = json.Unmarshal([]byte(frame.Msg), &m)
		return m
	}

	// 1. Untrusted hello -> hello-reply untrusted
	sendClientMsg(map[string]any{
		"type":      "hello",
		"nonce":     "nonce-123",
		"sessionId": pasteID,
		"browser":   "TestBrowser",
		"publicKey": clientPubB64,
	})
	reply := readPluginMsg()
	if reply["type"] != "hello-reply" || reply["state"] != "untrusted" {
		t.Fatalf("unexpected hello-reply: %v", reply)
	}

	// 2. Trust nonce -> receives trusted reply
	sock := lp.WebEditor.GetSocket(nil)
	if sock == nil || !sock.TrustConnection("nonce-123") {
		t.Fatal("TrustConnection failed")
	}
	reply = readPluginMsg()
	if reply["type"] != "hello-reply" || reply["state"] != "trusted" {
		t.Fatalf("expected trusted reply, got %v", reply)
	}

	// 3. Ping -> Pong
	sendClientMsg(map[string]any{"type": "ping"})
	reply = readPluginMsg()
	if reply["type"] != "pong" || reply["ok"] != true {
		t.Fatalf("expected pong, got %v", reply)
	}

	// 4. Upload a change response to bytebin and apply via ApplyWebEditorResponse (preserving concurrent edits!).
	// Add a concurrent node while session is open.
	g.SetNode(DataNormal, NewPermissionNode("concurrent.node", true))
	_ = lp.Storage.SaveGroup(g)

	respPayload := WebEditorResponsePayload{
		SessionID: pasteID,
		Changes: []WebEditorChange{{
			Type: "group",
			ID:   "vip",
			Nodes: []NodeJSON{
				{Key: "existing.node", Value: true},
				{Key: "editor.added", Value: true},
			},
		}},
	}
	if err := lp.ApplyWebEditorResponse(nil, "edit1", &respPayload, "lp", false); err != nil {
		t.Fatalf("ApplyWebEditorResponse: %v", err)
	}
	pd := g.CachedData().PermissionData(lp.Contexts.StaticQueryOptions())
	if !pd.CheckPermission("editor.added", OriginInternal).Result.AsBool() {
		t.Fatal("editor.added not applied")
	}
	if !pd.CheckPermission("concurrent.node", OriginInternal).Result.AsBool() {
		t.Fatal("concurrent.node was overwritten instead of diff-merged")
	}
	// Replaying without --force should fail.
	if err := lp.ApplyWebEditorResponse(nil, "edit1", &respPayload, "lp", false); err == nil {
		t.Fatal("expected error replaying completed session without force")
	}
}

func TestTranslationsManagerAndRepository(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/data/translations":
			_, _ = w.Write([]byte(`{"cacheMaxAge":3600,"languages":{"de":{"name":"German","localeTag":"de","progress":95,"contributors":[{"name":"Tester"}]}}}`))
		case "/translation/de":
			_, _ = w.Write([]byte("luckperms.commandsystem.no-permission=Keine Berechtigung!\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	lp := newUnitLuckPerms(t, "yaml")
	lp.Translations.customDir = filepath.Join(t.TempDir(), "custom")
	lp.Translations.repoDir = filepath.Join(t.TempDir(), "repo")
	lp.Translations.statusPath = filepath.Join(lp.Translations.repoDir, "status.json")
	lp.Translations.SetEndpoints(srv.URL+"/data/translations", srv.URL+"/translation/")
	lp.Translations.Reload()

	if got := lp.Translations.Get("en", "luckperms.commandsystem.no-permission"); !strings.Contains(got, "permission") {
		t.Fatalf("unexpected bundled en translation: %q", got)
	}
	langs, err := lp.Translations.AvailableLanguages()
	if err != nil || len(langs) != 1 || langs[0].LocaleTag != "de" {
		t.Fatalf("AvailableLanguages: %+v err=%v", langs, err)
	}
	n, err := lp.Translations.DownloadAndInstall(langs, nil)
	if err != nil || n != 1 {
		t.Fatalf("DownloadAndInstall: n=%d err=%v", n, err)
	}
	if got := lp.Translations.Get("de_DE", "luckperms.commandsystem.no-permission"); got != "Keine Berechtigung!" {
		t.Fatalf("de translation lookup failed: %q", got)
	}
}

package luckperms_test

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/golem-mc/golem/plugin"
	"github.com/golem-mc/perms"
	"github.com/golem-mc/perms/core"
	"github.com/golem-mc/perms/internal/lptest"
)

func TestPermissionProviderGroupsAndChildren(t *testing.T) {
	env := lptest.Start(t)
	_ = env.Join("Alice", false)

	// Register a parent permission with a child on the server.
	env.Srv.Host().Permissions().Register(plugin.Permission{
		Name:    "demo.bundle",
		Default: plugin.PermissionDefault{OpLevel: 1},
		Children: map[string]bool{
			"demo.bundle.child": true,
		},
	})

	p, ok := env.Srv.Host().PlayerNamed("Alice")
	if !ok {
		t.Fatal("Alice not online")
	}
	if p.HasPermission("demo.fly") {
		t.Fatal("non-op Alice should not have demo.fly initially")
	}

	// Create group vip and assign permissions via console commands.
	env.Expect(env.Console("lp creategroup vip 50 VIP"), "successfully created")
	env.Expect(env.Console("lp group vip permission set demo.fly true"), "Set demo.fly to true")
	env.Expect(env.Console("lp group vip permission set demo.chat.* true"), "Set demo.chat.* to true")
	env.Expect(env.Console("lp group vip permission set demo.chat.spam false"), "Set demo.chat.spam to false")
	env.Expect(env.Console("lp group vip permission set demo.bundle true"), "Set demo.bundle to true")
	env.Expect(env.Console("lp user Alice parent add vip"), "now inherits permissions from vip")

	if !p.HasPermission("demo.fly") || !p.PermissionSet("demo.fly") {
		t.Fatal("Alice should have demo.fly via vip group")
	}
	if !p.HasPermission("demo.chat.color") {
		t.Fatal("Alice should have demo.chat.color via wildcard")
	}
	if p.HasPermission("demo.chat.spam") {
		t.Fatal("Alice should be denied demo.chat.spam via explicit negation")
	}
	if !p.HasPermission("demo.bundle.child") {
		t.Fatal("Alice should inherit demo.bundle.child via ChildProcessor")
	}

	// User-level negation overrides group grant.
	env.Expect(env.Console("lp user Alice permission set demo.fly false"), "Set demo.fly to false")
	if p.HasPermission("demo.fly") {
		t.Fatal("user-level negation should override vip group grant")
	}

	// Permission check command reports false.
	env.Expect(env.Console("lp check Alice demo.fly"), "set to false")
}

func TestContextsWorldAndGameMode(t *testing.T) {
	env := lptest.Start(t)
	_ = env.Join("Alice", false)
	p, _ := env.Srv.Host().PlayerNamed("Alice")

	env.Expect(env.Console("lp user Alice permission set demo.overworld true world=world"), "Set demo.overworld")
	env.Expect(env.Console("lp user Alice permission set demo.nether true world=world_nether"), "Set demo.nether")
	env.Expect(env.Console("lp user Alice permission set demo.creative true gamemode=creative"), "Set demo.creative")

	if !p.HasPermission("demo.overworld") {
		t.Fatal("expected demo.overworld=true in overworld")
	}
	if p.HasPermission("demo.nether") {
		t.Fatal("expected demo.nether=false in overworld")
	}
	if p.HasPermission("demo.creative") {
		t.Fatal("expected demo.creative=false in survival")
	}

	// Switch Alice to creative mode and wait a tick for context refresh.
	env.Console("gamemode creative Alice")
	env.Srv.RunTicks(3)

	if !p.HasPermission("demo.creative") {
		t.Fatal("expected demo.creative=true after switching to creative mode")
	}
}

func TestTracksPromoteDemoteAndConvenienceAliases(t *testing.T) {
	env := lptest.Start(t)
	_ = env.Join("Bob", false)

	env.Expect(env.Console("lp creategroup helper 20"), "created")
	env.Expect(env.Console("lp creategroup mod 40"), "created")
	env.Expect(env.Console("lp creategroup admin 80"), "created")
	env.Expect(env.Console("lp createtrack staff"), "created")
	env.Expect(env.Console("lp track staff append helper"), "appended")
	env.Expect(env.Console("lp track staff append admin"), "appended")
	env.Expect(env.Console("lp track staff insert mod 2"), "inserted")
	env.Expect(env.Console("lp t staff info"), "helper ---> mod ---> admin")

	// Promote Bob along the track: none -> helper -> mod -> admin -> end of track.
	env.Expect(env.Console("lp u Bob promote staff"), "first group: helper")
	env.Expect(env.Console("lp u Bob promote staff"), "from helper to mod")
	env.Expect(env.Console("lp u Bob promote staff"), "from mod to admin")
	env.Expect(env.Console("lp u Bob promote staff"), "already at the end of track staff")

	// Show tracks
	env.Expect(env.Console("lp user Bob showtracks"), "staff:")

	// Demote back down.
	env.Expect(env.Console("lp user Bob demote staff"), "from admin to mod")
	env.Expect(env.Console("lp user Bob demote staff"), "from mod to helper")
	env.Expect(env.Console("lp user Bob demote staff"), "Removed Bob from helper")
}

func TestMetaPrefixSuffixAndAPIService(t *testing.T) {
	env := lptest.Start(t)
	_ = env.Join("Carol", false)
	p, _ := env.Srv.Host().PlayerNamed("Carol")

	api, ok := plugin.Service[luckperms.API](env.Srv.Host())
	if !ok {
		t.Fatal("luckperms API service not registered")
	}

	env.Expect(env.Console("lp creategroup vip 50"), "created")
	env.Expect(env.Console(`lp g vip m addprefix 100 "&a[VIP] "`), "Added prefix")
	env.Expect(env.Console(`lp g vip m addsuffix 100 " &e*"`), "Added suffix")
	env.Expect(env.Console(`lp g vip m set home-limit 5`), "Set meta value")
	env.Expect(env.Console("lp u Carol parent add vip"), "now inherits")

	if got := api.PrimaryGroup(p); got != "vip" {
		t.Fatalf("PrimaryGroup: got %q, want vip", got)
	}
	if got := api.Prefix(p); got != "&a[VIP] " {
		t.Fatalf("Prefix: got %q, want &a[VIP] ", got)
	}
	if got := api.Suffix(p); got != " &e*" {
		t.Fatalf("Suffix: got %q, want ' &e*'", got)
	}
	if v, ok := api.MetaValue(p, "home-limit"); !ok || v != "5" {
		t.Fatalf("MetaValue home-limit: got %q (%v), want 5", v, ok)
	}
	if got := api.CheckPermission(p, "group.vip"); got != core.TristateTrue {
		t.Fatalf("CheckPermission group.vip: got %v, want true", got)
	}
	env.Expect(env.Console("lp user Carol meta info"), "home-limit = 5")
}

func TestTemporaryNodesAndCommandPermissions(t *testing.T) {
	env := lptest.Start(t)
	dave := env.Join("Dave", false)
	p, _ := env.Srv.Host().PlayerNamed("Dave")

	// Non-op Dave without luckperms.* cannot create groups.
	env.Expect(env.Run(dave, "lp creategroup nope"), "do not have permission")

	// Grant Dave luckperms.creategroup temporarily, then he can!
	env.Expect(env.Console("lp user Dave permission settemp luckperms.creategroup true 1h"), "duration of")
	if !p.HasPermission("luckperms.creategroup") {
		t.Fatal("Dave should have temporary luckperms.creategroup")
	}
	env.Expect(env.Run(dave, "lp creategroup builders"), "successfully created")

	// Accumulate adds duration; unsettemp subtracts and removes.
	env.Expect(env.Console("lp user Dave permission settemp luckperms.creategroup true 30m accumulate"), "duration of")
	env.Expect(env.Console("lp user Dave permission unsettemp luckperms.creategroup 10m"), "Subtracted 10m")
	env.Expect(env.Console("lp user Dave permission unsettemp luckperms.creategroup"), "Unset temporary permission")
	if p.HasPermission("luckperms.creategroup") {
		t.Fatal("Dave should no longer have luckperms.creategroup")
	}
}

func TestLogVerboseBulkUpdateExportImport(t *testing.T) {
	env := lptest.Start(t)
	_ = env.Join("Eve", true)
	p, _ := env.Srv.Host().PlayerNamed("Eve")

	env.Expect(env.Console("lp creategroup testers 10"), "created")
	env.Expect(env.Console("lp group testers permission set test.alpha true"), "Set test.alpha")
	env.Expect(env.Console("lp group testers permission set test.beta true"), "Set test.beta")
	env.Expect(env.Console("lp user Eve parent add testers"), "now inherits")

	// Log & Search & Tree & Info & Sync & ReloadConfig
	env.Expect(env.Console("lp log recent"), "test.alpha")
	env.Expect(env.Console("lp log grouphistory testers"), "test.beta")
	env.Expect(env.Console("lp search ~~ test.%"), "test.alpha")
	env.Expect(env.Console("lp info"), "LuckPerms v")
	env.Expect(env.Console("lp sync"), "completed")
	env.Expect(env.Console("lp reloadconfig"), "reloaded")

	// Verbose on -> check -> off
	env.Expect(env.Console("lp verbose on Eve & test."), "Verbose logging enabled")
	_ = p.HasPermission("test.alpha")
	env.Expect(env.Console("lp verbose off"), "matched 1 of")
	env.Expect(env.Console("lp tree test"), "alpha")

	// Export -> BulkUpdate delete -> verify deleted -> Import --replace -> verify restored!
	env.Expect(env.Console("lp export backup1"), "Successfully exported")

	lines := env.Console(`lp bulkupdate groups delete "permission ~~ test.%"`)
	env.Expect(lines, "bulkupdate confirm")
	idRe := regexp.MustCompile(`confirm (\d{4})`)
	var confirmID string
	for _, l := range lines {
		if m := idRe.FindStringSubmatch(l); m != nil {
			confirmID = m[1]
		}
	}
	if confirmID == "" {
		t.Fatalf("did not find confirmation ID in %q", lines)
	}
	env.Expect(env.Console("lp bulkupdate confirm "+confirmID), "Affected 2 node(s)")
	if env.LP.Groups.GetIfLoaded("testers").CachedData().PermissionData(env.LP.Contexts.StaticQueryOptions()).CheckPermission("test.alpha", core.OriginInternal).Result == core.TristateTrue {
		t.Fatal("test.alpha should have been deleted by bulkupdate")
	}

	env.Expect(env.Console("lp import backup1 --replace"), "Import completed")
	if !env.LP.Groups.GetIfLoaded("testers").CachedData().PermissionData(env.LP.Contexts.StaticQueryOptions()).CheckPermission("test.alpha", core.OriginInternal).Result.AsBool() {
		t.Fatal("test.alpha should have been restored by import")
	}

	// Verify listgroups contains testers
	listOut := strings.Join(env.Console("lp listgroups"), "\n")
	if !strings.Contains(listOut, "testers") {
		t.Fatalf("expected testers in listgroups, got:\n%s", listOut)
	}
}

type memMessengerProvider struct {
	mu   sync.Mutex
	sent []string
}

func (p *memMessengerProvider) Name() string { return "InMemory" }
func (p *memMessengerProvider) Obtain(c core.IncomingMessageConsumer) (core.Messenger, error) {
	return &memMessenger{p: p}, nil
}

type memMessenger struct{ p *memMessengerProvider }

func (m *memMessenger) SendOutgoingMessage(encoded string) error {
	m.p.mu.Lock()
	m.p.sent = append(m.p.sent, encoded)
	m.p.mu.Unlock()
	return nil
}
func (m *memMessenger) Close() error { return nil }

func TestWebEditorUploadsTranslationsNetworkSyncAndSelectors(t *testing.T) {
	var mu sync.Mutex
	pastes := map[string][]byte{}
	seq := 0
	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			seq++
			key := "code" + string(rune('0'+seq))
			pastes[key] = body
			mu.Unlock()
			w.Header().Set("Location", key)
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/data/translations":
			_, _ = w.Write([]byte(`{"cacheMaxAge":3600,"languages":{"fr":{"name":"French","localeTag":"fr","progress":100,"contributors":[{"name":"Dev"}]}}}`))
		case r.URL.Path == "/translation/fr":
			_, _ = w.Write([]byte("luckperms.commandsystem.no-permission=Pas de permission\n"))
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
	defer mockSrv.Close()

	env := lptest.Start(t)
	_ = env.Join("Frank", false)
	_ = env.Join("Grace", false)
	pFrank, _ := env.Srv.Host().PlayerNamed("Frank")
	pGrace, _ := env.Srv.Host().PlayerNamed("Grace")

	// Point Bytebin & Translations at mock server (no live bytesocks).
	env.LP.Bytebin = core.NewBytebinClient(mockSrv.URL+"/", "lp-test")
	env.LP.Config().BytesocksURL = mockSrv.URL + "/no-socket/"
	env.LP.Translations.SetEndpoints(mockSrv.URL+"/data/translations", mockSrv.URL+"/translation/")

	// 1. /lp editor -> /lp applyedits
	edLines := env.Console("lp editor groups")
	env.Expect(edLines, "luckperms.net/editor/code1")

	editResp := core.WebEditorResponsePayload{
		SessionID: "code1",
		Changes: []core.WebEditorChange{{
			Type:  "group",
			ID:    "default",
			Nodes: []core.NodeJSON{{Key: "web.granted", Value: true}},
		}},
	}
	respBytes, _ := json.Marshal(editResp)
	mu.Lock()
	pastes["edit99"] = respBytes
	mu.Unlock()

	env.Expect(env.Console("lp applyedits edit99"), "Applied web editor changes to group default")
	if !pFrank.HasPermission("web.granted") {
		t.Fatal("Frank should have web.granted after /lp applyedits")
	}

	// 2. /lp verbose upload & /lp tree --web & /lp export --upload & /lp import --web
	env.Expect(env.Console("lp verbose record"), "Verbose recording enabled")
	_ = pFrank.HasPermission("web.granted")
	env.Expect(env.Console("lp verbose upload"), "luckperms.net/verbose/")
	env.Expect(env.Console("lp tree web --web"), "luckperms.net/treeview/")
	exportLines := env.Console("lp export --upload")
	env.Expect(exportLines, "Successfully uploaded export to bytebin")
	codeRe := regexp.MustCompile(`code: ([a-zA-Z0-9]+)`)
	var exportCode string
	for _, l := range exportLines {
		if m := codeRe.FindStringSubmatch(l); m != nil {
			exportCode = m[1]
		}
	}
	if exportCode == "" {
		t.Fatalf("missing export code in %q", exportLines)
	}
	env.Expect(env.Console("lp import --web "+exportCode), "Import completed")

	// 3. /lp translations & /lp translations install
	env.Expect(env.Console("lp translations"), "French")
	env.Expect(env.Console("lp translations install"), "Installed 1 translation bundle")

	// 4. Custom messenger + /lp networksync
	api, _ := plugin.Service[luckperms.API](env.Srv.Host())
	mp := &memMessengerProvider{}
	if err := api.RegisterMessengerProvider(mp); err != nil {
		t.Fatalf("RegisterMessengerProvider: %v", err)
	}
	env.Expect(env.Console("lp info"), "InMemory")
	env.Expect(env.Console("lp networksync"), "notified of the update via InMemory")
	mp.mu.Lock()
	sentCount := len(mp.sent)
	mp.mu.Unlock()
	if sentCount == 0 {
		t.Fatal("expected networksync to publish update message")
	}

	// 5. Target selector resolution (@a)
	env.LP.Config().ResolveCommandSelectors = true
	env.Expect(env.Console("lp user @a permission set demo.all true"), "Set demo.all to true")
	if !pFrank.HasPermission("demo.all") || !pGrace.HasPermission("demo.all") {
		t.Fatal("expected @a selector to grant demo.all to both Frank and Grace")
	}
}

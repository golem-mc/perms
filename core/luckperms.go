package core

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golem-mc/golem/plugin"
	"github.com/google/uuid"
)

// Version is the LuckPerms for golem plugin version string.
const Version = "5.5.0-golem"

// API is the service interface registered with golem's Services manager
// (`plugin.Service[core.API](s)`) so other plugins can inspect groups,
// prefixes, suffixes, meta, permissions, and register custom context calculators.
type API interface {
	PrimaryGroup(p plugin.Player) string
	Prefix(p plugin.Player) string
	Suffix(p plugin.Player) string
	MetaValue(p plugin.Player, key string) (string, bool)
	Meta(p plugin.Player) map[string][]string
	CheckPermission(p plugin.Player, perm string) Tristate
	Groups() []string
	UserGroups(p plugin.Player) []string
	RegisterContextCalculator(calc ContextCalculator)
	RegisterMessengerProvider(provider MessengerProvider) error
	Engine() *LuckPerms
}

// LuckPerms is the central runtime state of the plugin.
type LuckPerms struct {
	Server    plugin.Server
	Log       *plugin.Logger
	StartedAt time.Time
	dataDir   string

	cfgMu  sync.RWMutex
	config *Config

	Storage      Storage
	Messaging    *MessagingService
	Bytebin      *BytebinClient
	WebEditor    *WebEditorStore
	Translations *TranslationManager
	Users        *UserManager
	Groups       *GroupManager
	Tracks       *TrackManager
	Contexts     *ContextManager
	Actions      *ActionLogger
	Verbose      *VerboseHandler
	Registry     *PermissionRegistry

	syncTask    plugin.Task
	watcherTask plugin.Task
	auditTask   plugin.Task

	watchMu    sync.Mutex
	watchState map[string]time.Time
	stopping   atomic.Bool
}

var (
	instMu   sync.Mutex
	instance *LuckPerms
)

// Get returns the active LuckPerms instance, if enabled.
func Get() (*LuckPerms, bool) {
	instMu.Lock()
	defer instMu.Unlock()
	return instance, instance != nil
}

// Enable initializes LuckPerms for server s, loads storage, provides
// plugin.PermissionProvider and core.API services, and registers listeners.
func Enable(s plugin.Server) (*LuckPerms, error) {
	dataDir := s.DataDir()
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}
	cfg, err := LoadConfig(dataDir)
	if err != nil {
		return nil, fmt.Errorf("luckperms: load config: %w", err)
	}

	lp := &LuckPerms{
		Server:     s,
		Log:        s.Logger(),
		StartedAt:  time.Now(),
		dataDir:    dataDir,
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
	lp.Bytebin = NewBytebinClient(cfg.BytebinURL, "luckperms/"+Version)
	lp.WebEditor = newWebEditorStore(lp)
	lp.Translations = newTranslationManager(lp)

	storage, err := lp.createStorage(cfg)
	if err != nil {
		return nil, err
	}
	lp.Storage = storage
	if err := lp.Storage.Init(); err != nil {
		return nil, fmt.Errorf("luckperms: init storage (%s): %w", storage.Name(), err)
	}

	if err := lp.Storage.LoadAllGroups(); err != nil {
		return nil, fmt.Errorf("luckperms: load groups: %w", err)
	}
	if err := lp.Storage.LoadAllTracks(); err != nil {
		return nil, fmt.Errorf("luckperms: load tracks: %w", err)
	}

	lp.defineEvents()
	lp.registerPermissions()
	lp.initMessaging()

	if cfg.AutoInstallTranslations {
		s.Scheduler().RunAsync(func() {
			if langs, err := lp.Translations.AvailableLanguages(); err == nil {
				_, _ = lp.Translations.DownloadAndInstall(langs, nil)
			}
		})
	}

	// Provide golem's PermissionProvider service and LuckPerms's own API service.
	plugin.Provide[plugin.PermissionProvider](s, &permissionProvider{lp: lp}, 100)
	plugin.Provide[API](s, &apiService{lp: lp}, 100)
	lp.provideVault()

	lp.registerListeners()
	lp.startTasks()

	// Load any players already online (shouldn't happen on normal start, but safe).
	for _, p := range s.Players() {
		lp.loadOnlinePlayer(p)
	}

	instMu.Lock()
	instance = lp
	instMu.Unlock()

	lp.Log.Info("LuckPerms enabled", "version", Version, "storage", lp.Storage.Name(), "groups", len(lp.Groups.All()), "tracks", len(lp.Tracks.All()))
	return lp, nil
}

// Disable shuts down background tasks and flushes storage.
func (lp *LuckPerms) Disable() {
	lp.stopping.Store(true)
	if lp.syncTask != nil {
		lp.syncTask.Cancel()
	}
	if lp.watcherTask != nil {
		lp.watcherTask.Cancel()
	}
	if lp.auditTask != nil {
		lp.auditTask.Cancel()
	}
	if lp.WebEditor != nil {
		lp.WebEditor.CloseAllSockets()
	}
	if lp.Messaging != nil {
		lp.Messaging.Close()
	}
	if lp.Storage != nil {
		_ = lp.Storage.Shutdown()
	}
	instMu.Lock()
	if instance == lp {
		instance = nil
	}
	instMu.Unlock()
}

// DataDir returns the plugin's data directory.
func (lp *LuckPerms) DataDir() string {
	if lp.dataDir != "" {
		return lp.dataDir
	}
	if lp.Server == nil {
		return "."
	}
	return lp.Server.DataDir()
}

// Config returns the current immutable Config snapshot.
func (lp *LuckPerms) Config() *Config {
	lp.cfgMu.RLock()
	defer lp.cfgMu.RUnlock()
	return lp.config
}

// ReloadConfig rereads config.yml and contexts.json and refreshes caches.
func (lp *LuckPerms) ReloadConfig() error {
	cfg, err := LoadConfig(lp.DataDir())
	if err != nil {
		return err
	}
	lp.cfgMu.Lock()
	lp.config = cfg
	lp.cfgMu.Unlock()

	lp.registerPermissions()
	if lp.Translations != nil {
		lp.Translations.Reload()
	}
	lp.Contexts.InvalidateAll()
	lp.InvalidateAllCaches()
	lp.restartSyncAndWatchTasks()
	lp.refreshAllAutoOp()
	return nil
}

// Sync reloads all groups, tracks, and online users from storage.
func (lp *LuckPerms) Sync() error {
	if err := lp.Storage.LoadAllGroups(); err != nil {
		return err
	}
	if err := lp.Storage.LoadAllTracks(); err != nil {
		return err
	}
	for _, u := range lp.Users.Loaded() {
		if _, err := lp.Storage.LoadUser(u.UUID(), u.Username()); err != nil {
			lp.Log.Warn("failed to sync user", "uuid", u.UUID(), "err", err)
		}
	}
	lp.InvalidateAllCaches()
	lp.refreshAllAutoOp()
	return nil
}

// InvalidateAllCaches clears cached permission and meta data across all loaded groups and users.
func (lp *LuckPerms) InvalidateAllCaches() {
	for _, g := range lp.Groups.All() {
		if g.cached != nil {
			g.cached.Invalidate()
		}
	}
	for _, u := range lp.Users.Loaded() {
		if u.cached != nil {
			u.cached.Invalidate()
		}
		lp.fireUserDataRecalculate(u)
	}
	lp.refreshAllAutoOp()
}

func (lp *LuckPerms) onHolderCacheInvalidated(h *PermissionHolder) {
	if h.Type() == HolderUser {
		id := parseUUIDSilent(h.Identifier())
		if u := lp.Users.GetIfLoaded(id); u != nil {
			lp.fireUserDataRecalculate(u)
		}
		if lp.Server != nil {
			if p, ok := lp.Server.Player(id); ok {
				lp.refreshAutoOp(p)
			}
		}
	}
}

func (lp *LuckPerms) defaultQueryOptions(contexts ContextSet) QueryOptions {
	cfg := lp.Config()
	return QueryOptions{
		Mode:     QueryContextual,
		Contexts: contexts,
		Flags: QueryFlags{
			ResolveInheritance:                        true,
			IncludeNodesWithoutServerContext:          cfg.IncludeGlobal,
			IncludeNodesWithoutWorldContext:           cfg.IncludeGlobalWorld,
			ApplyInheritanceNodesWithoutServerContext: cfg.ApplyGlobalGroups,
			ApplyInheritanceNodesWithoutWorldContext:  cfg.ApplyGlobalWorldGroups,
		},
		ContextSatisfyMode: cfg.ContextSatisfyMode,
	}
}

func (lp *LuckPerms) createStorage(cfg *Config) (Storage, error) {
	if cfg.SplitStorage.Enabled {
		return NewSplitStorage(lp, cfg.SplitStorage)
	}
	return lp.createSingleStorage(cfg.StorageMethod)
}

func (lp *LuckPerms) createSingleStorage(method string) (Storage, error) {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "", "yaml", "yml":
		return NewFileStorage(lp, FormatYAML, false), nil
	case "yaml-combined", "yml-combined":
		return NewFileStorage(lp, FormatYAML, true), nil
	case "json":
		return NewFileStorage(lp, FormatJSON, false), nil
	case "json-combined":
		return NewFileStorage(lp, FormatJSON, true), nil
	case "toml":
		return NewFileStorage(lp, FormatTOML, false), nil
	case "toml-combined":
		return NewFileStorage(lp, FormatTOML, true), nil
	case "sqlite":
		return NewSQLStorage(lp, DialectSQLite), nil
	case "h2":
		lp.Log.Warn("H2 is a Java database; using SQLite instead in golem port")
		return NewSQLStorage(lp, DialectSQLite), nil
	case "mysql":
		return NewSQLStorage(lp, DialectMySQL), nil
	case "mariadb":
		return NewSQLStorage(lp, DialectMariaDB), nil
	case "postgresql", "postgres":
		return NewSQLStorage(lp, DialectPostgreSQL), nil
	case "mongodb", "mongo":
		return NewMongoStorage(lp), nil
	default:
		return nil, fmt.Errorf("luckperms: unsupported storage-method %q (supported: yaml, yaml-combined, json, json-combined, toml, toml-combined, sqlite, mysql, mariadb, postgresql, mongodb)", method)
	}
}

func (lp *LuckPerms) isOnline(id plugin.UUID) bool {
	if lp.Server == nil {
		return false
	}
	_, ok := lp.Server.Player(id)
	return ok
}

// ResolveUserUUID resolves a username or UUID string to (uuid, username, ok).
func (lp *LuckPerms) ResolveUserUUID(target string) (plugin.UUID, string, bool) {
	if id, err := uuid.Parse(target); err == nil {
		name, _ := lp.Storage.GetPlayerName(id)
		if name == "" && lp.Server != nil {
			if p, ok := lp.Server.Player(id); ok {
				name = p.Name()
			} else if op, ok := lp.Server.OfflinePlayer(id); ok {
				name = op.Name()
			}
		}
		return id, name, true
	}
	if lp.Server != nil {
		if p, ok := lp.Server.PlayerNamed(target); ok {
			return p.UUID(), p.Name(), true
		}
	}
	if id, ok := lp.Storage.GetPlayerUniqueID(target); ok {
		name, _ := lp.Storage.GetPlayerName(id)
		return id, name, true
	}
	if lp.Config().UseServerUUIDCache && lp.Server != nil {
		if op, ok := lp.Server.OfflinePlayerNamed(target); ok {
			return op.UUID(), op.Name(), true
		}
	}
	return plugin.UUID{}, "", false
}

// WithUser loads target (online or offline), runs fn, saves if modified, and unloads offline users afterwards.
func (lp *LuckPerms) WithUser(id plugin.UUID, username string, save bool, fn func(u *User) error) error {
	online := lp.isOnline(id)
	u := lp.Users.GetIfLoaded(id)
	loadedTemp := false
	if u == nil {
		var err error
		u, err = lp.Storage.LoadUser(id, username)
		if err != nil {
			return err
		}
		loadedTemp = !online
	}
	err := fn(u)
	if err == nil && save {
		err = lp.Storage.SaveUser(u)
	}
	if loadedTemp && !lp.isOnline(id) {
		lp.Users.Unload(id)
	}
	return err
}

// --- Golem Event Listeners & Tasks ---

func (lp *LuckPerms) registerListeners() {
	s := lp.Server

	// Pre-login (runs on connection goroutine): load user data & save uuid mapping.
	plugin.Listen(s, func(e *plugin.PlayerPreLoginEvent) {
		if e.Deny {
			return
		}
		if lp.Config().DebugLogins {
			lp.Log.Info("Processing pre-login", "uuid", e.Profile.ID, "name", e.Profile.Name)
		}
		_ = lp.Storage.SavePlayerData(e.Profile.ID, e.Profile.Name)
		if _, err := lp.Storage.LoadUser(e.Profile.ID, e.Profile.Name); err != nil {
			lp.Log.Error("Failed to load user data during pre-login", "uuid", e.Profile.ID, "name", e.Profile.Name, "err", err)
			e.Deny = true
			e.Reason = plugin.Plain("Permissions data could not be loaded. Please try again later.").Color("red")
		}
	}, plugin.ListenOptions{Priority: plugin.Low})

	// Login / Join (tick goroutine): ensure user loaded, refresh contexts & auto-op.
	plugin.Listen(s, func(e *plugin.PlayerLoginEvent) {
		if e.Result != plugin.LoginAllowed {
			return
		}
		lp.loadOnlinePlayer(e.Player)
	}, plugin.ListenOptions{Priority: plugin.Lowest})

	plugin.Listen(s, func(e *plugin.PlayerJoinEvent) {
		lp.Contexts.SignalContextUpdate(e.Player)
		lp.applyOpPolicyOnJoin(e.Player)
	}, plugin.ListenOptions{Priority: plugin.Lowest})

	// Quit (MONITOR): unload user 1 tick later so other MONITOR listeners can still check permissions.
	plugin.Listen(s, func(e *plugin.PlayerQuitEvent) {
		p := e.Player
		id := p.UUID()
		s.Scheduler().RunTask(func() {
			if lp.stopping.Load() {
				return
			}
			if _, stillOnline := s.Player(id); stillOnline {
				return
			}
			lp.Contexts.RemovePlayer(id)
			lp.Users.Unload(id)
			if lp.Config().AutoOp && p.IsValid() {
				p.SetOpLevel(0)
			}
		})
	}, plugin.ListenOptions{Priority: plugin.Monitor})

	// World & GameMode changes update contexts immediately.
	plugin.Listen(s, func(e *plugin.PlayerChangedWorldEvent) {
		lp.Contexts.SignalContextUpdate(e.Player)
	}, plugin.ListenOptions{Priority: plugin.Lowest})

	plugin.Listen(s, func(e *plugin.PlayerGameModeChangeEvent) {
		// PlayerGameModeChangeEvent fires before gamemode changes; refresh next tick.
		p := e.Player
		s.Scheduler().RunTask(func() {
			if p.IsValid() {
				lp.Contexts.SignalContextUpdate(p)
			}
		})
	}, plugin.ListenOptions{Priority: plugin.Monitor, IgnoreCancelled: true})

	// Block /op and /deop when enable-ops is false (or auto-op is true).
	plugin.Listen(s, func(e *plugin.PlayerCommandEvent) {
		cfg := lp.Config()
		if cfg.EnableOps && !cfg.AutoOp {
			return
		}
		cmd := strings.TrimSpace(e.Command)
		first, _, _ := strings.Cut(cmd, " ")
		first = strings.ToLower(first)
		if _, after, ok := strings.Cut(first, ":"); ok {
			first = after
		}
		if first == "op" || first == "deop" {
			e.SetCancelled(true)
			SendError(e.Player, "The /"+first+" command is disabled by LuckPerms (enable-ops is false).")
		}
	}, plugin.ListenOptions{Priority: plugin.Lowest, IgnoreCancelled: true})
}

func (lp *LuckPerms) loadOnlinePlayer(p plugin.Player) {
	id := p.UUID()
	name := p.Name()
	_ = lp.Storage.SavePlayerData(id, name)
	if u := lp.Users.GetIfLoaded(id); u == nil {
		if _, err := lp.Storage.LoadUser(id, name); err != nil {
			lp.Log.Error("Failed to load online player", "uuid", id, "name", name, "err", err)
		}
	} else {
		u.SetUsername(name)
	}
}

func (lp *LuckPerms) applyOpPolicyOnJoin(p plugin.Player) {
	cfg := lp.Config()
	if cfg.AutoOp {
		lp.refreshAutoOp(p)
	} else if !cfg.EnableOps && p.OpLevel() > 0 {
		p.SetOpLevel(0)
	}
}

func (lp *LuckPerms) refreshAutoOp(p plugin.Player) {
	if lp.stopping.Load() || p == nil || !p.IsValid() {
		return
	}
	cfg := lp.Config()
	if !cfg.AutoOp {
		return
	}
	u := lp.Users.GetIfLoaded(p.UUID())
	if u == nil {
		return
	}
	qo := lp.Contexts.QueryOptionsForPlayer(p)
	wantOp := u.CachedData().PermissionData(qo).CheckPermission("luckperms.autoop", OriginInternal).Result.AsBool()
	wantLevel := 0
	if wantOp {
		wantLevel = 4
	}
	if p.OpLevel() != wantLevel {
		// Schedule on tick goroutine to obey golem's threading rule.
		id := p.UUID()
		lp.Server.Scheduler().RunTask(func() {
			if pl, ok := lp.Server.Player(id); ok && pl.OpLevel() != wantLevel {
				pl.SetOpLevel(wantLevel)
			}
		})
	}
}

func (lp *LuckPerms) refreshAllAutoOp() {
	if lp.Server == nil || !lp.Config().AutoOp {
		return
	}
	for _, p := range lp.Server.Players() {
		lp.refreshAutoOp(p)
	}
}

func (lp *LuckPerms) startTasks() {
	// Audit expired temporary nodes every 30 seconds.
	lp.auditTask = lp.Server.Scheduler().RunAsyncTimer(30*time.Second, 30*time.Second, func() {
		if lp.stopping.Load() {
			return
		}
		for _, g := range lp.Groups.All() {
			if g.AuditTemporaryNodes() {
				_ = lp.Storage.SaveGroup(g)
			}
		}
		for _, u := range lp.Users.Loaded() {
			if u.AuditTemporaryNodes() {
				_ = lp.Storage.SaveUser(u)
			}
		}
	})
	lp.restartSyncAndWatchTasks()
}

func (lp *LuckPerms) restartSyncAndWatchTasks() {
	if lp.syncTask != nil {
		lp.syncTask.Cancel()
		lp.syncTask = nil
	}
	if lp.watcherTask != nil {
		lp.watcherTask.Cancel()
		lp.watcherTask = nil
	}
	cfg := lp.Config()
	if cfg.SyncMinutes > 0 {
		interval := time.Duration(cfg.SyncMinutes) * time.Minute
		lp.syncTask = lp.Server.Scheduler().RunAsyncTimer(interval, interval, func() {
			if !lp.stopping.Load() {
				_ = lp.Sync()
			}
		})
	}
	if cfg.WatchFiles && lp.Storage != nil && lp.Storage.DataDir() != "" {
		lp.snapshotWatchFiles()
		lp.watcherTask = lp.Server.Scheduler().RunAsyncTimer(5*time.Second, 5*time.Second, func() {
			if !lp.stopping.Load() && lp.detectWatchChanges() {
				lp.Log.Info("Detected external storage file change, syncing...")
				_ = lp.Sync()
			}
		})
	}
}

func (lp *LuckPerms) snapshotWatchFiles() {
	lp.watchMu.Lock()
	defer lp.watchMu.Unlock()
	lp.watchState = scanDirModTimes(lp.Storage.DataDir())
}

// recordInternalWrite updates watchState so LuckPerms's own saves don't trigger external-edit syncs.
func (lp *LuckPerms) recordInternalWrite(path string) {
	if lp == nil {
		return
	}
	lp.watchMu.Lock()
	defer lp.watchMu.Unlock()
	if info, err := os.Stat(path); err == nil {
		lp.watchState[path] = info.ModTime()
	} else {
		delete(lp.watchState, path)
	}
}

func (lp *LuckPerms) detectWatchChanges() bool {
	cur := scanDirModTimes(lp.Storage.DataDir())
	lp.watchMu.Lock()
	defer lp.watchMu.Unlock()
	if len(cur) != len(lp.watchState) {
		lp.watchState = cur
		return true
	}
	for k, t := range cur {
		if prev, ok := lp.watchState[k]; !ok || !prev.Equal(t) {
			lp.watchState = cur
			return true
		}
	}
	return false
}

func scanDirModTimes(root string) map[string]time.Time {
	res := make(map[string]time.Time)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if name == "uuidcache.txt" || name == "actions.json" || strings.HasSuffix(name, ".tmp") {
			return nil
		}
		if info, err := d.Info(); err == nil {
			res[path] = info.ModTime()
		}
		return nil
	})
	return res
}

// --- Golem PermissionProvider implementation ---

type permissionProvider struct {
	lp *LuckPerms
}

func (pp *permissionProvider) Has(sender plugin.CommandSender, node string) (has, decided bool) {
	p, ok := sender.(plugin.Player)
	if !ok {
		// Console / command block: leave to golem defaults.
		return false, false
	}
	u := pp.lp.Users.GetIfLoaded(p.UUID())
	if u == nil {
		return false, false
	}
	qo := pp.lp.Contexts.QueryOptionsForPlayer(p)
	qo.Op = p.OpLevel() > 0 && pp.lp.Config().EnableOps
	res := u.CachedData().PermissionData(qo).CheckPermission(node, OriginPlatformPermissionCheck)
	if res.Result != TristateUndefined {
		return res.Result.AsBool(), true
	}
	// If default permissions are disabled, explicitly deny undefined nodes (unless op and enable-ops).
	cfg := pp.lp.Config()
	if !cfg.ApplyDefaultPermissions {
		if cfg.EnableOps && p.OpLevel() > 0 {
			return true, true
		}
		return false, true
	}
	// If enable-ops is false, prevent golem from falling back to operator level.
	if !cfg.EnableOps && !cfg.AutoOp {
		if reg, ok := pp.lp.Server.Permissions().Get(node); ok && reg.Default.OpLevel == 0 {
			return true, true
		}
		return false, true
	}
	// Leave to golem's registered permission default & OpLevel fallback.
	return false, false
}

// --- Public API service implementation ---

type apiService struct {
	lp *LuckPerms
}

func (a *apiService) Engine() *LuckPerms { return a.lp }

func (a *apiService) PrimaryGroup(p plugin.Player) string {
	u := a.lp.Users.GetIfLoaded(p.UUID())
	if u == nil {
		return DefaultGroupName
	}
	return u.PrimaryGroup(a.lp.Contexts.QueryOptionsForPlayer(p))
}

func (a *apiService) Prefix(p plugin.Player) string {
	u := a.lp.Users.GetIfLoaded(p.UUID())
	if u == nil {
		return ""
	}
	return u.CachedData().MetaData(a.lp.Contexts.QueryOptionsForPlayer(p)).Prefix()
}

func (a *apiService) Suffix(p plugin.Player) string {
	u := a.lp.Users.GetIfLoaded(p.UUID())
	if u == nil {
		return ""
	}
	return u.CachedData().MetaData(a.lp.Contexts.QueryOptionsForPlayer(p)).Suffix()
}

func (a *apiService) MetaValue(p plugin.Player, key string) (string, bool) {
	u := a.lp.Users.GetIfLoaded(p.UUID())
	if u == nil {
		return "", false
	}
	return u.CachedData().MetaData(a.lp.Contexts.QueryOptionsForPlayer(p)).MetaValue(key)
}

func (a *apiService) Meta(p plugin.Player) map[string][]string {
	u := a.lp.Users.GetIfLoaded(p.UUID())
	if u == nil {
		return nil
	}
	return u.CachedData().MetaData(a.lp.Contexts.QueryOptionsForPlayer(p)).Meta()
}

func (a *apiService) CheckPermission(p plugin.Player, perm string) Tristate {
	u := a.lp.Users.GetIfLoaded(p.UUID())
	if u == nil {
		return TristateUndefined
	}
	return u.CachedData().PermissionData(a.lp.Contexts.QueryOptionsForPlayer(p)).CheckPermission(perm, OriginLuckPermsAPI).Result
}

func (a *apiService) Groups() []string { return a.lp.Groups.Names() }

func (a *apiService) UserGroups(p plugin.Player) []string {
	u := a.lp.Users.GetIfLoaded(p.UUID())
	if u == nil {
		return []string{DefaultGroupName}
	}
	qo := a.lp.Contexts.QueryOptionsForPlayer(p)
	holders := a.lp.traverseInheritance(&u.PermissionHolder, qo)
	var out []string
	for _, h := range holders {
		if h.Type() == HolderGroup {
			out = append(out, h.Identifier())
		}
	}
	return out
}

func (a *apiService) RegisterContextCalculator(calc ContextCalculator) {
	a.lp.Contexts.RegisterCalculator(calc)
}

func (a *apiService) RegisterMessengerProvider(provider MessengerProvider) error {
	ms, err := NewMessagingService(a.lp, provider)
	if err != nil {
		return err
	}
	if a.lp.Messaging != nil {
		a.lp.Messaging.Close()
	}
	a.lp.Messaging = ms
	return nil
}

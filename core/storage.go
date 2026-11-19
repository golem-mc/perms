package core

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/golem-mc/golem/plugin"
	"github.com/google/uuid"
	"go.yaml.in/yaml/v4"
)

// NodeSearchResult is a single match from SearchUserNodes / SearchGroupNodes.
type NodeSearchResult struct {
	HolderType HolderType
	HolderName string
	HolderUUID plugin.UUID
	Node       Node
}

// Storage is the persistence backend for LuckPerms.
type Storage interface {
	Name() string
	Init() error
	Shutdown() error

	LoadUser(id plugin.UUID, username string) (*User, error)
	SaveUser(u *User) error
	GetUniqueUsers() ([]plugin.UUID, error)
	SearchUserNodes(match func(Node) bool) ([]NodeSearchResult, error)

	CreateAndLoadGroup(name string) (*Group, error)
	LoadGroup(name string) (*Group, bool, error)
	LoadAllGroups() error
	SaveGroup(g *Group) error
	DeleteGroup(g *Group) error
	SearchGroupNodes(match func(Node) bool) ([]NodeSearchResult, error)

	CreateAndLoadTrack(name string) (*Track, error)
	LoadTrack(name string) (*Track, bool, error)
	LoadAllTracks() error
	SaveTrack(t *Track) error
	DeleteTrack(t *Track) error

	SavePlayerData(id plugin.UUID, username string) error
	DeletePlayerData(id plugin.UUID) error
	GetPlayerUniqueID(username string) (plugin.UUID, bool)
	GetPlayerName(id plugin.UUID) (string, bool)

	LogAction(a Action) error
	GetLogEntries() ([]Action, error)

	ApplyBulkUpdate(bu *BulkUpdate) error
	DataDir() string
}

// FileFormat selects YAML, JSON, or TOML file storage.
type FileFormat int

const (
	FormatYAML FileFormat = iota
	FormatJSON
	FormatTOML
)

// FileStorage implements Storage in LuckPerms's exact YAML/JSON/TOML separated or combined file formats.
type FileStorage struct {
	lp       *LuckPerms
	format   FileFormat
	combined bool
	dir      string

	mu            sync.Mutex
	uuidToName    map[plugin.UUID]string
	nameToUUID    map[string]plugin.UUID
	uuidCachePath string
	actionsPath   string
	actions       []Action
}

// NewFileStorage constructs a YAML, JSON, or TOML storage backend.
func NewFileStorage(lp *LuckPerms, format FileFormat, combined bool) *FileStorage {
	sub := "yaml-storage"
	switch format {
	case FormatJSON:
		sub = "json-storage"
	case FormatTOML:
		sub = "toml-storage"
	}
	return &FileStorage{
		lp:         lp,
		format:     format,
		combined:   combined,
		dir:        filepath.Join(lp.DataDir(), sub),
		uuidToName: make(map[plugin.UUID]string),
		nameToUUID: make(map[string]plugin.UUID),
	}
}

func (s *FileStorage) Name() string {
	base := "YAML"
	switch s.format {
	case FormatJSON:
		base = "JSON"
	case FormatTOML:
		base = "TOML"
	}
	if s.combined {
		return base + " (Combined)"
	}
	return base
}

func (s *FileStorage) DataDir() string { return s.dir }

func (s *FileStorage) ext() string {
	switch s.format {
	case FormatJSON:
		return ".json"
	case FormatTOML:
		return ".toml"
	default:
		return ".yml"
	}
}

func (s *FileStorage) Init() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	if !s.combined {
		for _, sub := range []string{"users", "groups", "tracks"} {
			if err := os.MkdirAll(filepath.Join(s.dir, sub), 0o755); err != nil {
				return err
			}
		}
	}
	s.uuidCachePath = filepath.Join(s.dir, "uuidcache.txt")
	s.loadUUIDCacheLocked()

	s.actionsPath = filepath.Join(s.dir, "actions.json")
	s.loadActionsLocked()
	return nil
}

func (s *FileStorage) Shutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveUUIDCacheLocked()
	return nil
}

// --- Low-level read/write helpers ---

func (s *FileStorage) marshalDoc(doc map[string]any) ([]byte, error) {
	switch s.format {
	case FormatJSON:
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(b, '\n'), nil
	case FormatTOML:
		var buf bytes.Buffer
		if err := toml.NewEncoder(&buf).Encode(doc); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return yaml.Marshal(doc)
	}
}

func (s *FileStorage) unmarshalDoc(data []byte) (map[string]any, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	switch s.format {
	case FormatJSON:
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
	case FormatTOML:
		if err := toml.Unmarshal(data, &out); err != nil {
			return nil, err
		}
	default:
		if err := yaml.Unmarshal(data, &out); err != nil {
			return nil, err
		}
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func (s *FileStorage) readDocLocked(kind, name string) (map[string]any, bool, error) {
	if s.combined {
		path := filepath.Join(s.dir, kind+s.ext())
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		root, err := s.unmarshalDoc(data)
		if err != nil {
			return nil, false, err
		}
		raw, ok := root[name]
		if !ok {
			return nil, false, nil
		}
		sub, ok := toStringMap(raw)
		if !ok {
			return nil, false, nil
		}
		return sub, true, nil
	}

	path := filepath.Join(s.dir, kind, name+s.ext())
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	doc, err := s.unmarshalDoc(data)
	if err != nil {
		return nil, false, err
	}
	return doc, true, nil
}

func (s *FileStorage) writeDocLocked(kind, name string, doc map[string]any) error {
	if s.combined {
		path := filepath.Join(s.dir, kind+s.ext())
		root := map[string]any{}
		if data, err := os.ReadFile(path); err == nil {
			if parsed, err := s.unmarshalDoc(data); err == nil {
				root = parsed
			}
		}
		if doc == nil {
			delete(root, name)
		} else {
			root[name] = doc
		}
		b, err := s.marshalDoc(root)
		if err != nil {
			return err
		}
		if err := atomicWriteFile(path, b); err != nil {
			return err
		}
		s.lp.recordInternalWrite(path)
		return nil
	}

	path := filepath.Join(s.dir, kind, name+s.ext())
	if doc == nil {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		s.lp.recordInternalWrite(path)
		return nil
	}
	b, err := s.marshalDoc(doc)
	if err != nil {
		return err
	}
	if err := atomicWriteFile(path, b); err != nil {
		return err
	}
	s.lp.recordInternalWrite(path)
	return nil
}

func (s *FileStorage) listNamesLocked(kind string) ([]string, error) {
	if s.combined {
		path := filepath.Join(s.dir, kind+s.ext())
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		root, err := s.unmarshalDoc(data)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(root))
		for k := range root {
			out = append(out, k)
		}
		slices.Sort(out)
		return out, nil
	}

	entries, err := os.ReadDir(filepath.Join(s.dir, kind))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ext := s.ext()
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if base, ok := strings.CutSuffix(e.Name(), ext); ok && base != "" {
			out = append(out, base)
		}
	}
	slices.Sort(out)
	return out, nil
}

func atomicWriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// --- LuckPerms YAML/JSON node serialization (100% compatible with AbstractConfigurateStorage.java) ---

func (s *FileStorage) encodeNodes(nodes []Node) map[string]any {
	doc := make(map[string]any)
	var perms, parents, prefixes, suffixes, meta []any

	sorted := slices.Clone(nodes)
	slices.SortStableFunc(sorted, CompareNodes)

	for _, n := range sorted {
		isPlain := n.Value && !n.HasExpiry() && n.Contexts.IsEmpty()
		if s.format == FormatYAML && isPlain {
			if n.Type == NodeTypeInheritance {
				parents = append(parents, n.GroupName)
				continue
			}
			if n.Type != NodeTypeMeta && n.Type != NodeTypePrefix && n.Type != NodeTypeSuffix {
				perms = append(perms, n.Key)
				continue
			}
		}

		switch {
		case (n.Type == NodeTypePrefix || n.Type == NodeTypeSuffix) && n.Value:
			attrs := map[string]any{"priority": n.Priority}
			writeAttrs(attrs, n, false)
			if n.Type == NodeTypePrefix {
				prefixes = append(prefixes, s.wrapEntry(n.MetaValue, "prefix", attrs))
			} else {
				suffixes = append(suffixes, s.wrapEntry(n.MetaValue, "suffix", attrs))
			}
		case n.Type == NodeTypeMeta && n.Value:
			attrs := map[string]any{"value": n.MetaValue}
			writeAttrs(attrs, n, false)
			meta = append(meta, s.wrapEntry(n.MetaKey, "key", attrs))
		case n.Type == NodeTypeInheritance && n.Value:
			attrs := map[string]any{}
			writeAttrs(attrs, n, false)
			parents = append(parents, s.wrapEntry(n.GroupName, "group", attrs))
		default:
			attrs := map[string]any{}
			writeAttrs(attrs, n, true)
			perms = append(perms, s.wrapEntry(n.Key, "permission", attrs))
		}
	}

	if len(perms) > 0 || s.combined {
		if perms == nil {
			perms = []any{}
		}
		doc["permissions"] = perms
	}
	if len(parents) > 0 {
		doc["parents"] = parents
	}
	if len(prefixes) > 0 {
		doc["prefixes"] = prefixes
	}
	if len(suffixes) > 0 {
		doc["suffixes"] = suffixes
	}
	if len(meta) > 0 {
		doc["meta"] = meta
	}
	return doc
}

func (s *FileStorage) wrapEntry(key, keyField string, attrs map[string]any) any {
	if s.format == FormatYAML && key != "" {
		return map[string]any{key: attrs}
	}
	out := make(map[string]any, len(attrs)+1)
	out[keyField] = key
	for k, v := range attrs {
		out[k] = v
	}
	return out
}

func writeAttrs(attrs map[string]any, n Node, includeValue bool) {
	if includeValue {
		attrs["value"] = n.Value
	}
	if n.HasExpiry() {
		attrs["expiry"] = n.Expiry.Unix()
	}
	if !n.Contexts.IsEmpty() {
		attrs["context"] = ContextSetToJSONMap(n.Contexts)
	}
}

func decodeNodes(doc map[string]any) []Node {
	var nodes []Node

	for _, item := range toSlice(doc["permissions"]) {
		if str, ok := item.(string); ok {
			if str != "" {
				nodes = append(nodes, ParseNode(str, true))
			}
			continue
		}
		key, attrs, ok := parseEntry(item, "permission")
		if !ok || key == "" {
			continue
		}
		val := asBool(attrs["value"], true)
		nodes = append(nodes, applyAttrs(ParseNode(key, val), attrs))
	}

	for _, item := range toSlice(doc["parents"]) {
		if str, ok := item.(string); ok {
			if str != "" {
				nodes = append(nodes, NewInheritanceNode(str))
			}
			continue
		}
		key, attrs, ok := parseEntry(item, "group")
		if !ok || key == "" {
			continue
		}
		nodes = append(nodes, applyAttrs(NewInheritanceNode(key), attrs))
	}

	for _, item := range toSlice(doc["prefixes"]) {
		key, attrs, ok := parseEntry(item, "prefix")
		if !ok {
			continue
		}
		prio := asInt(attrs["priority"], 0)
		nodes = append(nodes, applyAttrs(NewPrefixNode(prio, key), attrs))
	}

	for _, item := range toSlice(doc["suffixes"]) {
		key, attrs, ok := parseEntry(item, "suffix")
		if !ok {
			continue
		}
		prio := asInt(attrs["priority"], 0)
		nodes = append(nodes, applyAttrs(NewSuffixNode(prio, key), attrs))
	}

	for _, item := range toSlice(doc["meta"]) {
		key, attrs, ok := parseEntry(item, "key")
		if !ok || key == "" {
			continue
		}
		val := asString(attrs["value"], "null")
		nodes = append(nodes, applyAttrs(NewMetaNode(key, val), attrs))
	}

	return nodes
}

func parseEntry(item any, keyField string) (string, map[string]any, bool) {
	m, ok := toStringMap(item)
	if !ok || len(m) == 0 {
		return "", nil, false
	}
	// YAML shorthand: single map entry { "<key>": { attrs... } }
	if len(m) == 1 {
		for k, v := range m {
			if k != keyField && k != "" {
				if attrs, ok := toStringMap(v); ok {
					return k, attrs, true
				}
			}
		}
	}
	rawKey, ok := m[keyField]
	if !ok {
		return "", nil, false
	}
	return fmt.Sprint(rawKey), m, true
}

func applyAttrs(n Node, attrs map[string]any) Node {
	if exp := asInt64(attrs["expiry"], 0); exp > 0 {
		n = n.WithExpiry(time.Unix(exp, 0).UTC())
	}
	b := NewMutableContextSet()
	if ctxMap, ok := toStringMap(attrs["context"]); ok {
		b.AddAll(ContextSetFromJSONMap(ctxMap))
	}
	if srv := asString(attrs["server"], ""); srv != "" {
		b.Add(ContextKeyServer, srv)
	}
	if world := asString(attrs["world"], ""); world != "" {
		b.Add(ContextKeyWorld, world)
	}
	if ctx := b.Immutable(); !ctx.IsEmpty() {
		n = n.WithContexts(ctx)
	}
	return n
}

// --- User / Group / Track persistence ---

func (s *FileStorage) primaryGroupKey() string {
	if s.format == FormatJSON {
		return "primaryGroup"
	}
	return "primary-group"
}

func (s *FileStorage) LoadUser(id plugin.UUID, username string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u := s.lp.Users.GetOrMake(id, username)
	doc, exists, err := s.readDocLocked("users", id.String())
	if err != nil {
		return nil, err
	}
	if exists {
		storedName := asString(doc["name"], "")
		if storedName != "" && storedName != "null" && u.Username() == "" {
			u.SetUsername(storedName)
		}
		pg := asString(doc[s.primaryGroupKey()], "")
		if pg == "" {
			pg = asString(doc["primary-group"], asString(doc["primaryGroup"], DefaultGroupName))
		}
		u.SetStoredPrimaryGroup(pg)
		u.Data(DataNormal).SetContent(decodeNodes(doc))
		u.GiveDefaultIfNeeded()
		u.InvalidateCache()

		updatedName := u.Username() != "" && !strings.EqualFold(u.Username(), storedName)
		if updatedName || u.AuditTemporaryNodes() {
			_ = s.saveUserLocked(u)
		}
	} else {
		u.Data(DataNormal).SetContent(nil)
		u.SetStoredPrimaryGroup(DefaultGroupName)
		u.GiveDefaultIfNeeded()
		u.InvalidateCache()
	}
	return u, nil
}

func (s *FileStorage) SaveUser(u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveUserLocked(u)
}

func (s *FileStorage) saveUserLocked(u *User) error {
	if !u.IsNonDefaultUser() {
		return s.writeDocLocked("users", u.UUID().String(), nil)
	}
	doc := s.encodeNodes(u.NormalNodes())
	if !s.combined {
		doc["uuid"] = u.UUID().String()
	}
	name := u.Username()
	if name == "" {
		name = "null"
	}
	doc["name"] = name
	doc[s.primaryGroupKey()] = u.StoredPrimaryGroup()
	return s.writeDocLocked("users", u.UUID().String(), doc)
}

func (s *FileStorage) GetUniqueUsers() ([]plugin.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names, err := s.listNamesLocked("users")
	if err != nil {
		return nil, err
	}
	var ids []plugin.UUID
	for _, n := range names {
		if id, err := uuid.Parse(n); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (s *FileStorage) SearchUserNodes(match func(Node) bool) ([]NodeSearchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names, err := s.listNamesLocked("users")
	if err != nil {
		return nil, err
	}
	var out []NodeSearchResult
	for _, n := range names {
		id, err := uuid.Parse(n)
		if err != nil {
			continue
		}
		doc, ok, err := s.readDocLocked("users", n)
		if err != nil || !ok {
			continue
		}
		uname := asString(doc["name"], n)
		if uname == "" || uname == "null" {
			uname = n
		}
		for _, node := range decodeNodes(doc) {
			if match(node) {
				out = append(out, NodeSearchResult{
					HolderType: HolderUser,
					HolderName: uname,
					HolderUUID: id,
					Node:       node,
				})
			}
		}
	}
	return out, nil
}

func (s *FileStorage) CreateAndLoadGroup(name string) (*Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(name)
	g := s.lp.Groups.GetOrMake(key)
	doc, exists, err := s.readDocLocked("groups", key)
	if err != nil {
		return nil, err
	}
	if exists {
		g.Data(DataNormal).SetContent(decodeNodes(doc))
		g.InvalidateCache()
	} else {
		if err := s.saveGroupLocked(g); err != nil {
			return nil, err
		}
	}
	return g, nil
}

func (s *FileStorage) LoadGroup(name string) (*Group, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(name)
	doc, exists, err := s.readDocLocked("groups", key)
	if err != nil || !exists {
		return nil, false, err
	}
	g := s.lp.Groups.GetOrMake(key)
	g.Data(DataNormal).SetContent(decodeNodes(doc))
	g.InvalidateCache()
	return g, true, nil
}

func (s *FileStorage) LoadAllGroups() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	names, err := s.listNamesLocked("groups")
	if err != nil {
		return err
	}
	loaded := make(map[string]struct{}, len(names))
	for _, name := range names {
		key := strings.ToLower(name)
		doc, ok, err := s.readDocLocked("groups", key)
		if err != nil || !ok {
			continue
		}
		g := s.lp.Groups.GetOrMake(key)
		g.Data(DataNormal).SetContent(decodeNodes(doc))
		loaded[key] = struct{}{}
	}
	// Unload groups deleted on disk.
	for _, g := range s.lp.Groups.All() {
		if _, ok := loaded[g.Name()]; !ok && g.Name() != DefaultGroupName {
			s.lp.Groups.Unload(g.Name())
		}
	}
	// Ensure default group always exists.
	if _, ok := loaded[DefaultGroupName]; !ok {
		g := s.lp.Groups.GetOrMake(DefaultGroupName)
		_ = s.saveGroupLocked(g)
	}
	s.lp.InvalidateAllCaches()
	return nil
}

func (s *FileStorage) SaveGroup(g *Group) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveGroupLocked(g)
}

func (s *FileStorage) saveGroupLocked(g *Group) error {
	doc := s.encodeNodes(g.NormalNodes())
	if !s.combined {
		doc["name"] = g.Name()
	}
	return s.writeDocLocked("groups", g.Name(), doc)
}

func (s *FileStorage) DeleteGroup(g *Group) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writeDocLocked("groups", g.Name(), nil); err != nil {
		return err
	}
	s.lp.Groups.Unload(g.Name())
	s.lp.InvalidateAllCaches()
	return nil
}

func (s *FileStorage) SearchGroupNodes(match func(Node) bool) ([]NodeSearchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names, err := s.listNamesLocked("groups")
	if err != nil {
		return nil, err
	}
	var out []NodeSearchResult
	for _, n := range names {
		doc, ok, err := s.readDocLocked("groups", n)
		if err != nil || !ok {
			continue
		}
		for _, node := range decodeNodes(doc) {
			if match(node) {
				out = append(out, NodeSearchResult{
					HolderType: HolderGroup,
					HolderName: n,
					Node:       node,
				})
			}
		}
	}
	return out, nil
}

func (s *FileStorage) CreateAndLoadTrack(name string) (*Track, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(name)
	t := s.lp.Tracks.GetOrMake(key)
	doc, exists, err := s.readDocLocked("tracks", key)
	if err != nil {
		return nil, err
	}
	if exists {
		t.SetGroups(toStringSlice(doc["groups"]))
	} else {
		if err := s.saveTrackLocked(t); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func (s *FileStorage) LoadTrack(name string) (*Track, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(name)
	doc, exists, err := s.readDocLocked("tracks", key)
	if err != nil || !exists {
		return nil, false, err
	}
	t := s.lp.Tracks.GetOrMake(key)
	t.SetGroups(toStringSlice(doc["groups"]))
	return t, true, nil
}

func (s *FileStorage) LoadAllTracks() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	names, err := s.listNamesLocked("tracks")
	if err != nil {
		return err
	}
	loaded := make(map[string]struct{}, len(names))
	for _, name := range names {
		key := strings.ToLower(name)
		doc, ok, err := s.readDocLocked("tracks", key)
		if err != nil || !ok {
			continue
		}
		t := s.lp.Tracks.GetOrMake(key)
		t.SetGroups(toStringSlice(doc["groups"]))
		loaded[key] = struct{}{}
	}
	for _, t := range s.lp.Tracks.All() {
		if _, ok := loaded[t.Name()]; !ok {
			s.lp.Tracks.Unload(t.Name())
		}
	}
	return nil
}

func (s *FileStorage) SaveTrack(t *Track) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveTrackLocked(t)
}

func (s *FileStorage) saveTrackLocked(t *Track) error {
	groups := t.Groups()
	if groups == nil {
		groups = []string{}
	}
	doc := map[string]any{"groups": groups}
	if !s.combined {
		doc["name"] = t.Name()
	}
	return s.writeDocLocked("tracks", t.Name(), doc)
}

func (s *FileStorage) DeleteTrack(t *Track) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writeDocLocked("tracks", t.Name(), nil); err != nil {
		return err
	}
	s.lp.Tracks.Unload(t.Name())
	return nil
}

// --- UUID Cache (LuckPerms uuidcache.txt: "username=uuid") ---

func (s *FileStorage) loadUUIDCacheLocked() {
	f, err := os.Open(s.uuidCachePath)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, idStr, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if id, err := uuid.Parse(strings.TrimSpace(idStr)); err == nil {
			name = strings.TrimSpace(name)
			s.uuidToName[id] = name
			s.nameToUUID[strings.ToLower(name)] = id
		}
	}
}

func (s *FileStorage) saveUUIDCacheLocked() {
	var lines []string
	for id, name := range s.uuidToName {
		lines = append(lines, name+"="+id.String())
	}
	slices.Sort(lines)
	_ = atomicWriteFile(s.uuidCachePath, []byte(strings.Join(lines, "\n")+"\n"))
}

func (s *FileStorage) SavePlayerData(id plugin.UUID, username string) error {
	if username == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.uuidToName[id]
	if prev != "" && !strings.EqualFold(prev, username) {
		delete(s.nameToUUID, strings.ToLower(prev))
	}
	s.uuidToName[id] = username
	s.nameToUUID[strings.ToLower(username)] = id
	s.saveUUIDCacheLocked()
	return nil
}

func (s *FileStorage) DeletePlayerData(id plugin.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.uuidToName[id]; ok {
		delete(s.nameToUUID, strings.ToLower(prev))
		delete(s.uuidToName, id)
		s.saveUUIDCacheLocked()
	}
	return nil
}

func (s *FileStorage) GetPlayerUniqueID(username string) (plugin.UUID, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.nameToUUID[strings.ToLower(username)]
	return id, ok
}

func (s *FileStorage) GetPlayerName(id plugin.UUID) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, ok := s.uuidToName[id]
	return name, ok
}

// --- Action Log persistence (actions.json) ---

func (s *FileStorage) loadActionsLocked() {
	data, err := os.ReadFile(s.actionsPath)
	if err != nil {
		return
	}
	var actions []Action
	if json.Unmarshal(data, &actions) == nil {
		s.actions = actions
	}
}

func (s *FileStorage) LogAction(a Action) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actions = append(s.actions, a)
	b, err := json.MarshalIndent(s.actions, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(s.actionsPath, b)
}

func (s *FileStorage) GetLogEntries() ([]Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.actions)
	// Newest first, matching LuckPerms ActionComparator.
	slices.SortFunc(out, func(a, b Action) int {
		if c := cmp.Compare(b.Timestamp.UnixNano(), a.Timestamp.UnixNano()); c != 0 {
			return c
		}
		return cmp.Compare(a.TargetName, b.TargetName)
	})
	return out, nil
}

// --- BulkUpdate ---

func (s *FileStorage) ApplyBulkUpdate(bu *BulkUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if bu.DataType == BulkDataAll || bu.DataType == BulkDataGroups {
		names, err := s.listNamesLocked("groups")
		if err != nil {
			return err
		}
		for _, name := range names {
			doc, ok, err := s.readDocLocked("groups", name)
			if err != nil || !ok {
				continue
			}
			nodes := decodeNodes(doc)
			updated, changed := bu.Apply(nodes, HolderGroup)
			if changed {
				newDoc := s.encodeNodes(updated)
				if !s.combined {
					newDoc["name"] = name
				}
				if err := s.writeDocLocked("groups", name, newDoc); err != nil {
					return err
				}
			}
		}
	}

	if bu.DataType == BulkDataAll || bu.DataType == BulkDataUsers {
		names, err := s.listNamesLocked("users")
		if err != nil {
			return err
		}
		for _, name := range names {
			doc, ok, err := s.readDocLocked("users", name)
			if err != nil || !ok {
				continue
			}
			nodes := decodeNodes(doc)
			updated, changed := bu.Apply(nodes, HolderUser)
			if changed {
				newDoc := s.encodeNodes(updated)
				if !s.combined {
					newDoc["uuid"] = name
				}
				if v, ok := doc["name"]; ok {
					newDoc["name"] = v
				}
				if v, ok := doc[s.primaryGroupKey()]; ok {
					newDoc[s.primaryGroupKey()] = v
				}
				if err := s.writeDocLocked("users", name, newDoc); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// --- Type coercion helpers ---

func toStringMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[fmt.Sprint(k)] = val
		}
		return out, true
	default:
		return nil, false
	}
}

func toSlice(v any) []any {
	switch s := v.(type) {
	case []any:
		return s
	case []map[string]any:
		out := make([]any, len(s))
		for i, m := range s {
			out[i] = m
		}
		return out
	case []string:
		out := make([]any, len(s))
		for i, str := range s {
			out[i] = str
		}
		return out
	default:
		return nil
	}
}

func toStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return slices.Clone(s)
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return nil
	}
}

func asString(v any, def string) string {
	if v == nil {
		return def
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func asBool(v any, def bool) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		if parsed, err := strconv.ParseBool(b); err == nil {
			return parsed
		}
	}
	return def
}

func asInt(v any, def int) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		if parsed, err := strconv.Atoi(n); err == nil {
			return parsed
		}
	}
	return def
}

func asInt64(v any, def int64) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case string:
		if parsed, err := strconv.ParseInt(n, 10, 64); err == nil {
			return parsed
		}
	}
	return def
}

func parseUUIDSilent(s string) plugin.UUID {
	id, _ := uuid.Parse(s)
	return id
}

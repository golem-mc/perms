package core

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/golem-mc/golem/plugin"
)

// DefaultContextKeys matches net.luckperms.api.context.DefaultContextKeys.
const (
	ContextKeyServer        = "server"
	ContextKeyWorld         = "world"
	ContextKeyDimensionType = "dimension-type"
	ContextKeyGameMode      = "gamemode"
	GlobalContextValue      = "global"
)

// ContextSatisfyMode controls how ContextSet.Satisfies matches multi-valued keys.
type ContextSatisfyMode int

const (
	// AtLeastOneValuePerKey: set A is satisfied by B if for every key in A,
	// at least one value of that key in A is present in B.
	AtLeastOneValuePerKey ContextSatisfyMode = iota
	// AllValuesPerKey: set A is satisfied by B if every (key, value) pair in A
	// is present in B.
	AllValuesPerKey
)

// Context is a single key-value context pair.
type Context struct {
	Key   string
	Value string
}

// IsValidContextKey reports whether k is a valid LuckPerms context key.
func IsValidContextKey(k string) bool {
	if k == "" {
		return false
	}
	return !strings.ContainsAny(k, " \t\n\r=")
}

// IsValidContextValue reports whether v is a valid LuckPerms context value.
func IsValidContextValue(v string) bool {
	if v == "" {
		return false
	}
	return !strings.ContainsAny(v, " \t\n\r=")
}

// ContextSet is an immutable multi-map of lowercase context keys to lowercase values.
// Its zero value is the empty context set.
type ContextSet struct {
	entries []Context // sorted, deduplicated
}

// EmptyContextSet is a ContextSet with no entries.
var EmptyContextSet ContextSet

// NewContextSet constructs an immutable ContextSet from entries.
// "server=global" and "world=global" are omitted, matching LuckPerms's ImmutableContextSetImpl.
func NewContextSet(entries ...Context) ContextSet {
	if len(entries) == 0 {
		return EmptyContextSet
	}
	b := NewMutableContextSet()
	for _, e := range entries {
		b.Add(e.Key, e.Value)
	}
	return b.Immutable()
}

// ParseContextSet parses key=value tokens (or positional server, world) into a ContextSet.
func ParseContextSet(tokens []string) ContextSet {
	b := NewMutableContextSet()
	for i, entry := range tokens {
		var k, v string
		if key, val, ok := strings.Cut(entry, "="); ok {
			k, v = key, val
		} else {
			if i == 1 {
				k = ContextKeyWorld
			} else {
				k = ContextKeyServer
			}
			v = entry
		}
		if IsValidContextKey(k) && IsValidContextValue(v) {
			b.Add(k, v)
		}
	}
	return b.Immutable()
}

// IsEmpty reports whether the set has no entries.
func (cs ContextSet) IsEmpty() bool { return len(cs.entries) == 0 }

// Len returns the number of key-value pairs.
func (cs ContextSet) Len() int { return len(cs.entries) }

// Entries returns a copy of all context entries in canonical sorted order.
func (cs ContextSet) Entries() []Context {
	return slices.Clone(cs.entries)
}

// Contains reports whether (key, value) is present (case-insensitive).
func (cs ContextSet) Contains(key, value string) bool {
	k := strings.ToLower(key)
	v := strings.ToLower(value)
	for _, e := range cs.entries {
		if e.Key == k && e.Value == v {
			return true
		}
	}
	return false
}

// ContainsKey reports whether any value exists for key.
func (cs ContextSet) ContainsKey(key string) bool {
	k := strings.ToLower(key)
	for _, e := range cs.entries {
		if e.Key == k {
			return true
		}
	}
	return false
}

// Values returns all values associated with key.
func (cs ContextSet) Values(key string) []string {
	k := strings.ToLower(key)
	var out []string
	for _, e := range cs.entries {
		if e.Key == k {
			out = append(out, e.Value)
		}
	}
	return out
}

// AnyValue returns the first value associated with key, if any.
func (cs ContextSet) AnyValue(key string) (string, bool) {
	k := strings.ToLower(key)
	for _, e := range cs.entries {
		if e.Key == k {
			return e.Value, true
		}
	}
	return "", false
}

// Equal reports exact equality of two ContextSets.
func (cs ContextSet) Equal(other ContextSet) bool {
	return slices.Equal(cs.entries, other.entries)
}

// Compare orders two ContextSets deterministically.
func (cs ContextSet) Compare(other ContextSet) int {
	if c := cmp.Compare(len(cs.entries), len(other.entries)); c != 0 {
		return c
	}
	for i := range cs.entries {
		if c := cmp.Compare(cs.entries[i].Key, other.entries[i].Key); c != 0 {
			return c
		}
		if c := cmp.Compare(cs.entries[i].Value, other.entries[i].Value); c != 0 {
			return c
		}
	}
	return 0
}

// IsSatisfiedBy reports whether this (node's required) ContextSet is satisfied
// by active (subject's active contexts) under the given mode.
func (cs ContextSet) IsSatisfiedBy(active ContextSet, mode ContextSatisfyMode) bool {
	if len(cs.entries) == 0 {
		return true
	}
	if len(active.entries) == 0 {
		return false
	}
	switch mode {
	case AllValuesPerKey:
		for _, req := range cs.entries {
			if !active.Contains(req.Key, req.Value) {
				return false
			}
		}
		return true
	default: // AtLeastOneValuePerKey
		// entries are sorted by Key, so we can scan group by group.
		i := 0
		for i < len(cs.entries) {
			key := cs.entries[i].Key
			matched := false
			for i < len(cs.entries) && cs.entries[i].Key == key {
				if !matched && active.Contains(key, cs.entries[i].Value) {
					matched = true
				}
				i++
			}
			if !matched {
				return false
			}
		}
		return true
	}
}

// MutableCopy returns a mutable copy of cs.
func (cs ContextSet) MutableCopy() *MutableContextSet {
	m := NewMutableContextSet()
	for _, e := range cs.entries {
		m.Add(e.Key, e.Value)
	}
	return m
}

// Map returns a map[string][]string representation.
func (cs ContextSet) Map() map[string][]string {
	out := make(map[string][]string)
	for _, e := range cs.entries {
		out[e.Key] = append(out[e.Key], e.Value)
	}
	return out
}

// String renders contexts as "k1=v1 k2=v2" or "global" if empty.
func (cs ContextSet) String() string {
	if len(cs.entries) == 0 {
		return GlobalContextValue
	}
	parts := make([]string, len(cs.entries))
	for i, e := range cs.entries {
		parts[i] = e.Key + "=" + e.Value
	}
	return strings.Join(parts, " ")
}

// Key returns a unique string suitable as a map key.
func (cs ContextSet) MapKey() string {
	if len(cs.entries) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, e := range cs.entries {
		if i > 0 {
			sb.WriteByte('\x00')
		}
		sb.WriteString(e.Key)
		sb.WriteByte('=')
		sb.WriteString(e.Value)
	}
	return sb.String()
}

// MutableContextSet builds a ContextSet.
type MutableContextSet struct {
	data map[string]map[string]struct{}
}

// NewMutableContextSet creates an empty MutableContextSet.
func NewMutableContextSet() *MutableContextSet {
	return &MutableContextSet{data: make(map[string]map[string]struct{})}
}

// Add inserts a (key, value) pair, normalizing to lowercase and dropping
// server=global / world=global as LuckPerms does.
func (m *MutableContextSet) Add(key, value string) {
	k := strings.ToLower(strings.TrimSpace(key))
	v := strings.ToLower(strings.TrimSpace(value))
	if k == "" || v == "" {
		return
	}
	if (k == ContextKeyServer || k == ContextKeyWorld) && v == GlobalContextValue {
		return
	}
	s := m.data[k]
	if s == nil {
		s = make(map[string]struct{})
		m.data[k] = s
	}
	s[v] = struct{}{}
}

// AddAll copies all entries from cs into m.
func (m *MutableContextSet) AddAll(cs ContextSet) {
	for _, e := range cs.entries {
		m.Add(e.Key, e.Value)
	}
}

// Remove deletes a specific (key, value) pair.
func (m *MutableContextSet) Remove(key, value string) {
	k := strings.ToLower(key)
	v := strings.ToLower(value)
	if s, ok := m.data[k]; ok {
		delete(s, v)
		if len(s) == 0 {
			delete(m.data, k)
		}
	}
}

// RemoveAll deletes all values for key.
func (m *MutableContextSet) RemoveAll(key string) {
	delete(m.data, strings.ToLower(key))
}

// Immutable freezes m into a sorted immutable ContextSet.
func (m *MutableContextSet) Immutable() ContextSet {
	if len(m.data) == 0 {
		return EmptyContextSet
	}
	var entries []Context
	for k, vals := range m.data {
		for v := range vals {
			entries = append(entries, Context{Key: k, Value: v})
		}
	}
	slices.SortFunc(entries, func(a, b Context) int {
		if c := cmp.Compare(a.Key, b.Key); c != 0 {
			return c
		}
		return cmp.Compare(a.Value, b.Value)
	})
	return ContextSet{entries: entries}
}

// ContextCalculator contributes contexts for a player.
type ContextCalculator interface {
	Calculate(player plugin.Player, consumer func(key, value string))
	EstimatePotentialContexts() ContextSet
}

// ContextCalculatorFunc adapts a plain function to ContextCalculator.
type ContextCalculatorFunc func(player plugin.Player, consumer func(key, value string))

func (f ContextCalculatorFunc) Calculate(player plugin.Player, consumer func(key, value string)) {
	f(player, consumer)
}
func (f ContextCalculatorFunc) EstimatePotentialContexts() ContextSet { return EmptyContextSet }

// ContextManager computes and caches QueryOptions and ContextSets for players.
type ContextManager struct {
	lp *LuckPerms

	mu          sync.RWMutex
	calculators []ContextCalculator
	cache       map[plugin.UUID]ContextSet
}

func newContextManager(lp *LuckPerms) *ContextManager {
	cm := &ContextManager{
		lp:    lp,
		cache: make(map[plugin.UUID]ContextSet),
	}
	cm.RegisterCalculator(&playerContextCalculator{lp: lp})
	return cm
}

// RegisterCalculator adds a custom ContextCalculator.
func (cm *ContextManager) RegisterCalculator(calc ContextCalculator) {
	if calc == nil {
		return
	}
	cm.mu.Lock()
	cm.calculators = append(cm.calculators, calc)
	clear(cm.cache)
	cm.mu.Unlock()
}

// StaticContexts returns the static contexts configured for this server
// (config.yml `server:` + `contexts.json` static-contexts).
func (cm *ContextManager) StaticContexts() ContextSet {
	cfg := cm.lp.Config()
	m := NewMutableContextSet()
	if cfg.Server != "" && !strings.EqualFold(cfg.Server, GlobalContextValue) {
		m.Add(ContextKeyServer, cfg.Server)
	}
	m.AddAll(cfg.StaticContexts)
	return m.Immutable()
}

// StaticQueryOptions returns the default QueryOptions for non-player or offline lookups.
func (cm *ContextManager) StaticQueryOptions() QueryOptions {
	return cm.lp.defaultQueryOptions(cm.StaticContexts())
}

// ContextsForPlayer returns the active ContextSet for an online player.
func (cm *ContextManager) ContextsForPlayer(p plugin.Player) ContextSet {
	if p == nil || !p.IsValid() {
		return cm.StaticContexts()
	}
	id := p.UUID()
	cm.mu.RLock()
	cached, ok := cm.cache[id]
	calcs := slices.Clone(cm.calculators)
	cm.mu.RUnlock()
	if ok {
		return cached
	}

	m := cm.StaticContexts().MutableCopy()
	consumer := func(k, v string) {
		m.Add(k, v)
	}
	for _, c := range calcs {
		c.Calculate(p, consumer)
	}
	res := m.Immutable()
	cm.mu.Lock()
	cm.cache[id] = res
	cm.mu.Unlock()
	return res
}

// QueryOptionsForPlayer returns QueryOptions for an online player.
func (cm *ContextManager) QueryOptionsForPlayer(p plugin.Player) QueryOptions {
	return cm.lp.defaultQueryOptions(cm.ContextsForPlayer(p))
}

// SignalContextUpdate clears cached contexts for p and triggers auto-op refresh if enabled.
func (cm *ContextManager) SignalContextUpdate(p plugin.Player) {
	if p == nil {
		return
	}
	cm.mu.Lock()
	delete(cm.cache, p.UUID())
	cm.mu.Unlock()
	if u := cm.lp.Users.GetIfLoaded(p.UUID()); u != nil {
		u.InvalidateCache()
	}
	cm.lp.refreshAutoOp(p)
}

// InvalidateAll clears all cached player contexts.
func (cm *ContextManager) InvalidateAll() {
	cm.mu.Lock()
	clear(cm.cache)
	cm.mu.Unlock()
}

// RemovePlayer drops cached context for a disconnected player.
func (cm *ContextManager) RemovePlayer(id plugin.UUID) {
	cm.mu.Lock()
	delete(cm.cache, id)
	cm.mu.Unlock()
}

// PotentialContexts estimates all known contexts for tab completion.
func (cm *ContextManager) PotentialContexts() ContextSet {
	m := cm.StaticContexts().MutableCopy()
	cm.mu.RLock()
	calcs := slices.Clone(cm.calculators)
	cm.mu.RUnlock()
	for _, c := range calcs {
		m.AddAll(c.EstimatePotentialContexts())
	}
	return m.Immutable()
}

// playerContextCalculator computes gamemode, world (with world-rewrite), and dimension-type.
type playerContextCalculator struct {
	lp *LuckPerms
}

func (c *playerContextCalculator) Calculate(p plugin.Player, accept func(key, value string)) {
	cfg := c.lp.Config()
	if !cfg.IsContextDisabled(ContextKeyGameMode) {
		accept(ContextKeyGameMode, GameModeName(p.GameMode()))
	}
	if w := p.World(); w != nil {
		if !cfg.IsContextDisabled(ContextKeyDimensionType) {
			accept(ContextKeyDimensionType, DimensionTypeName(w))
		}
		if !cfg.IsContextDisabled(ContextKeyWorld) {
			name := WorldName(w)
			if name != "" {
				rewriteWorld(name, cfg.WorldRewrites, accept)
			}
		}
	}
}

func (c *playerContextCalculator) EstimatePotentialContexts() ContextSet {
	cfg := c.lp.Config()
	m := NewMutableContextSet()
	if !cfg.IsContextDisabled(ContextKeyGameMode) {
		for _, gm := range []string{"survival", "creative", "adventure", "spectator"} {
			m.Add(ContextKeyGameMode, gm)
		}
	}
	if !cfg.IsContextDisabled(ContextKeyDimensionType) {
		for _, dim := range []string{"overworld", "the_nether", "the_end"} {
			m.Add(ContextKeyDimensionType, dim)
		}
	}
	if !cfg.IsContextDisabled(ContextKeyWorld) && c.lp.Server != nil {
		for _, w := range c.lp.Server.Worlds() {
			if name := WorldName(w); IsValidContextValue(name) {
				m.Add(ContextKeyWorld, name)
			}
		}
		for target := range maps.Values(cfg.WorldRewrites) {
			if IsValidContextValue(target) {
				m.Add(ContextKeyWorld, target)
			}
		}
	}
	return m.Immutable()
}

// rewriteWorld submits world and any recursive world-rewrite aliases, matching LuckPerms WorldRewriteMap.
func rewriteWorld(world string, rewrites map[string]string, accept func(key, value string)) {
	visited := make(map[string]struct{}, 2)
	cur := strings.ToLower(world)
	for cur != "" {
		if _, seen := visited[cur]; seen {
			break
		}
		visited[cur] = struct{}{}
		accept(ContextKeyWorld, cur)
		next, ok := rewrites[cur]
		if !ok {
			break
		}
		cur = strings.ToLower(next)
	}
}

// WorldName maps a golem World to the Bukkit-compatible world name used by LuckPerms and Essentials.
func WorldName(w plugin.World) string {
	if w == nil {
		return ""
	}
	k := w.Key()
	if k.Namespace == "minecraft" {
		switch k.Path {
		case "overworld":
			return "world"
		case "the_nether":
			return "world_nether"
		case "the_end":
			return "world_the_end"
		}
		return k.Path
	}
	return k.String()
}

// DimensionTypeName returns the dimension-type context value ("overworld", "the_nether", "the_end", or key).
func DimensionTypeName(w plugin.World) string {
	if w == nil {
		return ""
	}
	d := w.Dimension()
	if d.Namespace == "minecraft" {
		return d.Path
	}
	return d.String()
}

// GameModeName returns the lowercase LuckPerms gamemode context value.
func GameModeName(gm plugin.GameMode) string {
	switch gm {
	case plugin.Survival:
		return "survival"
	case plugin.Creative:
		return "creative"
	case plugin.Adventure:
		return "adventure"
	case plugin.Spectator:
		return "spectator"
	default:
		return "survival"
	}
}

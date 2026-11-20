package core

import (
	"cmp"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/golem-mc/golem/plugin"
)

// regexCache caches compiled regular expressions for RegexProcessor.
var regexCache sync.Map // map[string]*regexp.Regexp (nil if invalid)

func compileRegexCached(pattern string) *regexp.Regexp {
	if v, ok := regexCache.Load(pattern); ok {
		rx, _ := v.(*regexp.Regexp)
		return rx
	}
	// Java Pattern.matcher(perm).matches() anchors the whole string!
	rx, err := regexp.Compile("^(?:" + pattern + ")$")
	if err != nil {
		regexCache.Store(pattern, (*regexp.Regexp)(nil))
		return nil
	}
	regexCache.Store(pattern, rx)
	return rx
}

// PermissionProcessor evaluates a permission node against a single resolution layer.
type PermissionProcessor interface {
	Name() string
	HasPermission(perm string) TristateResult
}

// DirectProcessor checks exact permission matches in sourceMap.
type DirectProcessor struct {
	source map[string]Node
}

func (p *DirectProcessor) Name() string { return "DirectProcessor" }

func (p *DirectProcessor) HasPermission(perm string) TristateResult {
	if n, ok := p.source[perm]; ok {
		nc := n
		return TristateResult{Result: TristateOf(n.Value), Node: &nc, Processor: p.Name()}
	}
	return UndefinedResult
}

// ChildProcessor expands registered plugin.Permission children from sourceMap.
type ChildProcessor struct {
	children map[string]TristateResult
}

func newChildProcessor(srv plugin.Server, source map[string]Node) *ChildProcessor {
	cp := &ChildProcessor{children: make(map[string]TristateResult)}
	if srv == nil {
		return cp
	}
	perms := srv.Permissions()
	for k, n := range source {
		if perm, ok := perms.Get(k); ok && len(perm.Children) > 0 {
			nc := n
			visited := map[string]struct{}{k: {}}
			cp.expand(perms, perm.Children, n.Value, &nc, visited)
		}
	}
	return cp
}

func (p *ChildProcessor) expand(perms plugin.Permissions, children map[string]bool, parentVal bool, cause *Node, visited map[string]struct{}) {
	for childName, grant := range children {
		lc := strings.ToLower(childName)
		if _, seen := visited[lc]; seen {
			continue
		}
		visited[lc] = struct{}{}
		// Bukkit child semantics: if parent is true, child gets grant; if parent is false, child gets !grant.
		effective := grant
		if !parentVal {
			effective = !grant
		}
		if _, exists := p.children[lc]; !exists {
			p.children[lc] = TristateResult{Result: TristateOf(effective), Node: cause, Processor: "ChildProcessor"}
		}
		if sub, ok := perms.Get(childName); ok && len(sub.Children) > 0 {
			p.expand(perms, sub.Children, effective, cause, visited)
		}
	}
}

func (p *ChildProcessor) Name() string { return "ChildProcessor" }

func (p *ChildProcessor) HasPermission(perm string) TristateResult {
	if r, ok := p.children[perm]; ok {
		return r
	}
	return UndefinedResult
}

// RegexProcessor checks "r=<pattern>" nodes against perm.
type RegexProcessor struct {
	entries []regexEntry
}

type regexEntry struct {
	rx   *regexp.Regexp
	node Node
}

func newRegexProcessor(source map[string]Node) *RegexProcessor {
	var entries []regexEntry
	for _, n := range source {
		if n.Type == NodeTypeRegexPermission {
			if rx := compileRegexCached(n.Pattern); rx != nil {
				entries = append(entries, regexEntry{rx: rx, node: n})
			}
		}
	}
	return &RegexProcessor{entries: entries}
}

func (p *RegexProcessor) Name() string { return "RegexProcessor" }

func (p *RegexProcessor) HasPermission(perm string) TristateResult {
	for i := range p.entries {
		if p.entries[i].rx.MatchString(perm) {
			nc := p.entries[i].node
			return TristateResult{Result: TristateOf(nc.Value), Node: &nc, Processor: p.Name()}
		}
	}
	return UndefinedResult
}

// WildcardProcessor checks "a.b.*", "a.*", and "*" / "'*'".
type WildcardProcessor struct {
	wildcards map[string]TristateResult
	root      TristateResult
}

func newWildcardProcessor(source map[string]Node) *WildcardProcessor {
	wp := &WildcardProcessor{
		wildcards: make(map[string]TristateResult),
		root:      UndefinedResult,
	}
	for k, n := range source {
		if k == "*" || k == "'*'" {
			if wp.root.Result == TristateUndefined {
				nc := n
				wp.root = TristateResult{Result: TristateOf(n.Value), Node: &nc, Processor: "WildcardProcessor"}
			}
			continue
		}
		if len(k) > 2 && strings.HasSuffix(k, ".*") {
			prefix := k[:len(k)-2]
			nc := n
			wp.wildcards[prefix] = TristateResult{Result: TristateOf(n.Value), Node: &nc, Processor: "WildcardProcessor"}
		}
	}
	return wp
}

func (p *WildcardProcessor) Name() string { return "WildcardProcessor" }

func (p *WildcardProcessor) HasPermission(perm string) TristateResult {
	node := perm
	for {
		idx := strings.LastIndexByte(node, '.')
		if idx == -1 {
			break
		}
		node = node[:idx]
		if node != "" {
			if res, ok := p.wildcards[node]; ok && res.Result != TristateUndefined {
				return res
			}
		}
	}
	return p.root
}

// SpongeWildcardProcessor implements Sponge-style implicit wildcards ("a" grants "a.b.c").
type SpongeWildcardProcessor struct {
	source map[string]Node
}

func (p *SpongeWildcardProcessor) Name() string { return "SpongeWildcardProcessor" }

func (p *SpongeWildcardProcessor) HasPermission(perm string) TristateResult {
	node := perm
	for {
		idx := strings.LastIndexByte(node, '.')
		if idx == -1 {
			break
		}
		node = node[:idx]
		if node != "" {
			if n, ok := p.source[node]; ok {
				nc := n
				return TristateResult{Result: TristateOf(n.Value), Node: &nc, Processor: p.Name()}
			}
		}
	}
	return UndefinedResult
}

// DefaultNegatedProcessor checks if a registered permission has OpLevel >= 5 (false/nobody default)
// and overrides wildcards when apply-default-negated-permissions-before-wildcards is enabled.
type DefaultNegatedProcessor struct {
	srv plugin.Server
}

func (p *DefaultNegatedProcessor) Name() string { return "DefaultPermissionProcessor" }

func (p *DefaultNegatedProcessor) HasPermission(perm string) TristateResult {
	if p.srv == nil {
		return UndefinedResult
	}
	if reg, ok := p.srv.Permissions().Get(perm); ok && reg.Default.OpLevel >= 5 {
		return TristateResult{Result: TristateFalse, Processor: p.Name()}
	}
	return UndefinedResult
}

// PermissionData holds a holder's resolved permission map and calculator for a QueryOptions.
type PermissionData struct {
	lp         *LuckPerms
	holder     *PermissionHolder
	qo         QueryOptions
	sourceMap  map[string]Node
	processors []PermissionProcessor

	mu    sync.RWMutex
	cache map[string]TristateResult
}

func newPermissionData(lp *LuckPerms, holder *PermissionHolder, qo QueryOptions) *PermissionData {
	cfg := lp.Config()
	source := holder.ExportPermissions(qo, cfg.ApplyShorthand)

	var procs []PermissionProcessor
	procs = append(procs, &DirectProcessor{source: source})
	if cfg.ApplyChildPermissions {
		procs = append(procs, newChildProcessor(lp.Server, source))
	}
	if cfg.ApplyRegex {
		procs = append(procs, newRegexProcessor(source))
	}
	if cfg.ApplyDefaultPermissions && cfg.ApplyDefaultNegBeforeWild && holder.Type() == HolderUser {
		procs = append(procs, &DefaultNegatedProcessor{srv: lp.Server})
	}
	if cfg.ApplyWildcards {
		procs = append(procs, newWildcardProcessor(source))
	}
	if cfg.ApplySpongeWildcards {
		procs = append(procs, &SpongeWildcardProcessor{source: source})
	}

	return &PermissionData{
		lp:         lp,
		holder:     holder,
		qo:         qo,
		sourceMap:  source,
		processors: procs,
		cache:      make(map[string]TristateResult),
	}
}

// CheckPermission evaluates perm (case-insensitive) and records verbose/registry telemetry.
func (pd *PermissionData) CheckPermission(perm string, origin CheckOrigin) TristateResult {
	lc := strings.ToLower(perm)
	pd.lp.Registry.Offer(lc)

	pd.mu.RLock()
	res, ok := pd.cache[lc]
	pd.mu.RUnlock()

	if !ok {
		res = UndefinedResult
		for _, proc := range pd.processors {
			r := proc.HasPermission(lc)
			if r.Result != TristateUndefined {
				res = r
				break
			}
		}
		pd.mu.Lock()
		pd.cache[lc] = res
		pd.mu.Unlock()
	}

	if pd.lp.Verbose.IsListening() {
		targetName := pd.holder.Identifier()
		if pd.holder.Type() == HolderUser {
			if u := pd.lp.Users.GetIfLoaded(parseUUIDSilent(targetName)); u != nil {
				targetName = u.FriendlyName()
			}
		}
		pd.lp.Verbose.OfferPermissionCheck(targetName, pd.holder.Type(), pd.qo.Contexts, lc, res, origin)
	}
	return res
}

// PermissionMap returns a copy of the flattened permission map (key -> value).
func (pd *PermissionData) PermissionMap() map[string]bool {
	out := make(map[string]bool, len(pd.sourceMap))
	for k, n := range pd.sourceMap {
		out[k] = n.Value
	}
	return out
}

// --- Meta Accumulation & Prefix/Suffix Stacking ---

// MetaData holds accumulated meta, prefixes, suffixes, weight, and primary group for a QueryOptions.
type MetaData struct {
	lp           *LuckPerms
	meta         map[string][]StringResult
	prefixes     map[int]StringResult
	suffixes     map[int]StringResult
	prefix       StringResult
	suffix       StringResult
	weight       IntegerResult
	primaryGroup string
}

func newMetaData(lp *LuckPerms, holder *PermissionHolder, qo QueryOptions) *MetaData {
	cfg := lp.Config()
	md := &MetaData{
		lp:       lp,
		meta:     make(map[string][]StringResult),
		prefixes: make(map[int]StringResult),
		suffixes: make(map[int]StringResult),
	}

	prefixStack := newMetaStack(lp, cfg.PrefixFormatting, NodeTypePrefix)
	suffixStack := newMetaStack(lp, cfg.SuffixFormatting, NodeTypeSuffix)
	seenKeys := make(map[string]struct{})

	holders := lp.traverseInheritance(holder, qo)
	if !qo.Flags.ResolveInheritance {
		holders = []*PermissionHolder{holder}
	}

	for _, h := range holders {
		for _, n := range h.OwnNodes(qo) {
			if n.Type != NodeTypeMeta && n.Type != NodeTypePrefix && n.Type != NodeTypeSuffix {
				continue
			}
			lk := strings.ToLower(n.Key)
			if _, seen := seenKeys[lk]; seen {
				continue
			}
			seenKeys[lk] = struct{}{}
			if !n.Value {
				continue
			}
			nc := n
			switch n.Type {
			case NodeTypeMeta:
				md.meta[n.MetaKey] = append(md.meta[n.MetaKey], StringResult{Value: n.MetaValue, Has: true, Node: &nc})
			case NodeTypePrefix:
				if _, exists := md.prefixes[n.Priority]; !exists {
					md.prefixes[n.Priority] = StringResult{Value: n.MetaValue, Has: true, Node: &nc}
				}
				prefixStack.offer(nc)
			case NodeTypeSuffix:
				if _, exists := md.suffixes[n.Priority]; !exists {
					md.suffixes[n.Priority] = StringResult{Value: n.MetaValue, Has: true, Node: &nc}
				}
				suffixStack.offer(nc)
			}
		}
		if w := h.WeightResult(); w.Has {
			if !md.weight.Has || w.Value > md.weight.Value {
				md.weight = w
			}
		}
	}

	if holder.Type() == HolderUser {
		if u := lp.Users.GetIfLoaded(parseUUIDSilent(holder.Identifier())); u != nil {
			md.primaryGroup = u.PrimaryGroup(qo)
		}
	}

	// LuckPerms injects weight & primarygroup as virtual meta if not already set.
	if _, ok := md.meta["weight"]; !ok && md.weight.Has {
		md.meta["weight"] = []StringResult{{Value: strconv.Itoa(md.weight.Value), Has: true}}
	}
	if _, ok := md.meta["primarygroup"]; !ok && md.primaryGroup != "" {
		md.meta["primarygroup"] = []StringResult{{Value: md.primaryGroup, Has: true}}
	}

	md.prefix = prefixStack.result()
	md.suffix = suffixStack.result()
	return md
}

// Prefix returns the formatted prefix (empty string if none).
func (md *MetaData) Prefix() string { return md.prefix.Value }

// Suffix returns the formatted suffix (empty string if none).
func (md *MetaData) Suffix() string { return md.suffix.Value }

// Prefixes returns priority -> prefix value.
func (md *MetaData) Prefixes() map[int]string {
	out := make(map[int]string, len(md.prefixes))
	for k, v := range md.prefixes {
		out[k] = v.Value
	}
	return out
}

// Suffixes returns priority -> suffix value.
func (md *MetaData) Suffixes() map[int]string {
	out := make(map[int]string, len(md.suffixes))
	for k, v := range md.suffixes {
		out[k] = v.Value
	}
	return out
}

// PrimaryGroup returns the calculated primary group (for users).
func (md *MetaData) PrimaryGroup() string { return md.primaryGroup }

// Weight returns the highest inherited weight and whether one exists.
func (md *MetaData) Weight() (int, bool) { return md.weight.Value, md.weight.Has }

// Meta returns all meta values grouped by lowercase key.
func (md *MetaData) Meta() map[string][]string {
	out := make(map[string][]string, len(md.meta))
	for k, vals := range md.meta {
		s := make([]string, len(vals))
		for i, v := range vals {
			s[i] = v.Value
		}
		out[k] = s
	}
	return out
}

// MetaValue selects a single meta value for key using config `meta-value-selection`.
func (md *MetaData) MetaValue(key string) (string, bool) {
	vals := md.meta[strings.ToLower(key)]
	if len(vals) == 0 {
		return "", false
	}
	sel := md.lp.Config().MetaSelectionFor(key)
	switch sel {
	case MetaSelectHighestNumber:
		bestIdx := -1
		bestVal := -math.MaxFloat64
		for i, v := range vals {
			if f, err := strconv.ParseFloat(v.Value, 64); err == nil {
				if bestIdx == -1 || f > bestVal {
					bestIdx = i
					bestVal = f
				}
			}
		}
		if bestIdx != -1 {
			return vals[bestIdx].Value, true
		}
	case MetaSelectLowestNumber:
		bestIdx := -1
		bestVal := math.MaxFloat64
		for i, v := range vals {
			if f, err := strconv.ParseFloat(v.Value, 64); err == nil {
				if bestIdx == -1 || f < bestVal {
					bestIdx = i
					bestVal = f
				}
			}
		}
		if bestIdx != -1 {
			return vals[bestIdx].Value, true
		}
	}
	return vals[0].Value, true
}

// --- MetaStack (Prefix/Suffix stacking) ---

type metaStackSlot struct {
	match   func(n Node, cur *Node) bool
	current *Node
}

type metaStack struct {
	cfg   MetaStackConfig
	slots []metaStackSlot
}

func newMetaStack(lp *LuckPerms, cfg MetaStackConfig, targetType NodeType) *metaStack {
	ms := &metaStack{cfg: cfg}
	for _, spec := range cfg.Format {
		if fn := parseMetaStackElement(lp, spec, targetType); fn != nil {
			ms.slots = append(ms.slots, metaStackSlot{match: fn})
		}
	}
	return ms
}

func (ms *metaStack) offer(n Node) {
	for i := range ms.slots {
		if ms.slots[i].match(n, ms.slots[i].current) {
			nc := n
			ms.slots[i].current = &nc
		}
	}
}

func (ms *metaStack) result() StringResult {
	var parts []string
	var firstNode *Node
	for _, s := range ms.slots {
		if s.current != nil {
			if firstNode == nil {
				firstNode = s.current
			}
			parts = append(parts, s.current.MetaValue)
		}
	}
	if len(parts) == 0 {
		return StringResult{}
	}
	switch ms.cfg.Duplicates {
	case DuplicateFirstOnly:
		seen := make(map[string]struct{}, len(parts))
		dedup := parts[:0]
		for _, p := range parts {
			if _, ok := seen[p]; !ok {
				seen[p] = struct{}{}
				dedup = append(dedup, p)
			}
		}
		parts = dedup
	case DuplicateLastOnly:
		seen := make(map[string]struct{}, len(parts))
		var rev []string
		for _, part := range slices.Backward(parts) {
			if _, ok := seen[part]; !ok {
				seen[part] = struct{}{}
				rev = append(rev, part)
			}
		}
		slices.Reverse(rev)
		parts = rev
	}
	if len(parts) == 0 {
		return StringResult{}
	}
	formatted := ms.cfg.StartSpacer + strings.Join(parts, ms.cfg.MiddleSpacer) + ms.cfg.EndSpacer
	return StringResult{Value: formatted, Has: true, Node: firstNode}
}

func parseMetaStackElement(lp *LuckPerms, spec string, targetType NodeType) func(n Node, cur *Node) bool {
	s := strings.ToLower(strings.TrimSpace(spec))
	isHighest := func(n Node, cur *Node) bool { return cur == nil || n.Priority > cur.Priority }
	isLowest := func(n Node, cur *Node) bool { return cur == nil || n.Priority < cur.Priority }
	wrap := func(prio func(Node, *Node) bool, filter func(Node) bool) func(Node, *Node) bool {
		return func(n Node, cur *Node) bool {
			if n.Type != targetType {
				return false
			}
			if filter != nil && !filter(n) {
				return false
			}
			return prio(n, cur)
		}
	}

	switch s {
	case "highest":
		return wrap(isHighest, nil)
	case "lowest":
		return wrap(isLowest, nil)
	case "highest_own":
		return wrap(isHighest, func(n Node) bool { return n.Origin.Type == HolderUser })
	case "lowest_own":
		return wrap(isLowest, func(n Node) bool { return n.Origin.Type == HolderUser })
	case "highest_inherited":
		return wrap(isHighest, func(n Node) bool { return n.Origin.Type == HolderGroup })
	case "lowest_inherited":
		return wrap(isLowest, func(n Node) bool { return n.Origin.Type == HolderGroup })
	}

	onTrack := func(trackName string, want bool) func(Node) bool {
		return func(n Node) bool {
			tr := lp.Tracks.GetIfLoaded(trackName)
			if tr == nil {
				return false
			}
			in := n.Origin.Type == HolderGroup && tr.ContainsGroup(n.Origin.Name)
			return in == want
		}
	}
	fromGroup := func(groupName string, want bool) func(Node) bool {
		return func(n Node) bool {
			in := n.Origin.Type == HolderGroup && strings.EqualFold(n.Origin.Name, groupName)
			return in == want
		}
	}

	if p, ok := strings.CutPrefix(s, "highest_on_track_"); ok && p != "" {
		return wrap(isHighest, onTrack(p, true))
	}
	if p, ok := strings.CutPrefix(s, "lowest_on_track_"); ok && p != "" {
		return wrap(isLowest, onTrack(p, true))
	}
	if p, ok := strings.CutPrefix(s, "highest_not_on_track_"); ok && p != "" {
		return wrap(isHighest, onTrack(p, false))
	}
	if p, ok := strings.CutPrefix(s, "lowest_not_on_track_"); ok && p != "" {
		return wrap(isLowest, onTrack(p, false))
	}
	if p, ok := strings.CutPrefix(s, "highest_from_group_"); ok && p != "" {
		return wrap(isHighest, fromGroup(p, true))
	}
	if p, ok := strings.CutPrefix(s, "lowest_from_group_"); ok && p != "" {
		return wrap(isLowest, fromGroup(p, true))
	}
	if p, ok := strings.CutPrefix(s, "highest_not_from_group_"); ok && p != "" {
		return wrap(isHighest, fromGroup(p, false))
	}
	if p, ok := strings.CutPrefix(s, "lowest_not_from_group_"); ok && p != "" {
		return wrap(isLowest, fromGroup(p, false))
	}
	return nil
}

// --- CachedData ---

// CachedData caches computed PermissionData and MetaData per QueryOptions for a holder.
type CachedData struct {
	lp     *LuckPerms
	holder *PermissionHolder

	mu    sync.RWMutex
	perms map[string]*PermissionData
	meta  map[string]*MetaData
}

func newCachedData(lp *LuckPerms, holder *PermissionHolder) *CachedData {
	return &CachedData{
		lp:     lp,
		holder: holder,
		perms:  make(map[string]*PermissionData),
		meta:   make(map[string]*MetaData),
	}
}

// PermissionData returns cached PermissionData for qo, building it on demand.
func (cd *CachedData) PermissionData(qo QueryOptions) *PermissionData {
	key := qo.CacheKey()
	cd.mu.RLock()
	pd, ok := cd.perms[key]
	cd.mu.RUnlock()
	if ok {
		return pd
	}
	pd = newPermissionData(cd.lp, cd.holder, qo)
	cd.mu.Lock()
	cd.perms[key] = pd
	cd.mu.Unlock()
	return pd
}

// MetaData returns cached MetaData for qo, building it on demand.
func (cd *CachedData) MetaData(qo QueryOptions) *MetaData {
	key := qo.CacheKey()
	cd.mu.RLock()
	md, ok := cd.meta[key]
	cd.mu.RUnlock()
	if ok {
		return md
	}
	md = newMetaData(cd.lp, cd.holder, qo)
	cd.mu.Lock()
	cd.meta[key] = md
	cd.mu.Unlock()
	return md
}

// Invalidate clears all cached permission and meta data.
func (cd *CachedData) Invalidate() {
	cd.mu.Lock()
	clear(cd.perms)
	clear(cd.meta)
	cd.mu.Unlock()
}

// SortIntMapKeys returns sorted descending keys of a map[int]string (for prefix/suffix display).
func SortIntMapKeysDesc(m map[int]string) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b int) int { return cmp.Compare(b, a) })
	return keys
}

package core

import (
	"cmp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golem-mc/golem/plugin"
)

// DefaultGroupName is the mandatory default group every user inherits when they have no groups.
const DefaultGroupName = "default"

// DataType distinguishes persistent (normal) vs in-memory (transient) nodes.
type DataType int

const (
	DataNormal DataType = iota
	DataTransient
)

// MutateResult reports the outcome of a node mutation.
type MutateResult int

const (
	MutateSuccess MutateResult = iota
	MutateFail
	MutateFailAlreadyHas
	MutateFailLacks
)

// WasSuccessful reports whether the mutation succeeded.
func (r MutateResult) WasSuccessful() bool { return r == MutateSuccess }

// NodeMap stores a holder's nodes for a given DataType, thread-safely.
type NodeMap struct {
	mu    sync.RWMutex
	nodes []Node
}

// List returns a snapshot of all nodes.
func (m *NodeMap) List() []Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Clone(m.nodes)
}

// Len returns the number of nodes.
func (m *NodeMap) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.nodes)
}

// SetContent replaces all nodes, deduplicating by exact equality.
func (m *NodeMap) SetContent(nodes []Node) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodes = m.nodes[:0]
	for _, n := range nodes {
		if !m.containsLocked(n, EqualityExact) {
			m.nodes = append(m.nodes, n)
		}
	}
}

func (m *NodeMap) containsLocked(target Node, eq NodeEquality) bool {
	for _, n := range m.nodes {
		if n.Equal(target, eq) {
			return true
		}
	}
	return false
}

// Add inserts node if not already present (under EqualityExact).
// Returns MutateFailAlreadyHas if a matching node already exists.
func (m *NodeMap) Add(node Node) MutateResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.containsLocked(node, EqualityExact) {
		return MutateFailAlreadyHas
	}
	// Replace any existing node with the same key+contexts+expiry (different value).
	for i, existing := range m.nodes {
		if existing.Equal(node, EqualityIgnoreValue) {
			m.nodes[i] = node
			return MutateSuccess
		}
	}
	m.nodes = append(m.nodes, node)
	return MutateSuccess
}

// AddTemp inserts a temporary node using the specified merge strategy.
// Returns the resulting stored node and the MutateResult.
func (m *NodeMap) AddTemp(node Node, strategy TemporaryNodeMergeStrategy) (Node, MutateResult) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, existing := range m.nodes {
		if !existing.HasExpiry() {
			continue
		}
		if existing.Equal(node, EqualityIgnoreExpiry) {
			switch strategy {
			case TempMergeAccumulate:
				rem := max(time.Until(node.Expiry), 0)
				merged := existing.WithExpiry(existing.Expiry.Add(rem))
				m.nodes[i] = merged
				return merged, MutateSuccess
			case TempMergeReplace:
				if node.Expiry.After(existing.Expiry) {
					m.nodes[i] = node
					return node, MutateSuccess
				}
				return existing, MutateFailAlreadyHas
			default: // TempMergeDeny
				return existing, MutateFailAlreadyHas
			}
		}
	}
	m.nodes = append(m.nodes, node)
	return node, MutateSuccess
}

// Remove deletes nodes matching target under eq. Returns MutateFailLacks if none matched.
func (m *NodeMap) Remove(target Node, eq NodeEquality) MutateResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	before := len(m.nodes)
	m.nodes = slices.DeleteFunc(m.nodes, func(n Node) bool {
		return n.Equal(target, eq)
	})
	if len(m.nodes) == before {
		return MutateFailLacks
	}
	return MutateSuccess
}

// RemoveTemp subtracts duration from a matching temporary node (or removes it completely if duration <= 0
// or remaining duration <= 0). Returns the updated node (if still present), whether it was removed entirely, and MutateResult.
func (m *NodeMap) RemoveTemp(target Node, subtract time.Duration) (updated *Node, removed bool, res MutateResult) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, existing := range m.nodes {
		if !existing.HasExpiry() {
			continue
		}
		if existing.Equal(target, EqualityIgnoreExpiryAndValue) {
			if subtract <= 0 {
				m.nodes = slices.Delete(m.nodes, i, i+1)
				return nil, true, MutateSuccess
			}
			newExpiry := existing.Expiry.Add(-subtract)
			if !newExpiry.After(time.Now()) {
				m.nodes = slices.Delete(m.nodes, i, i+1)
				return nil, true, MutateSuccess
			}
			updatedNode := existing.WithExpiry(newExpiry)
			m.nodes[i] = updatedNode
			return &updatedNode, false, MutateSuccess
		}
	}
	return nil, false, MutateFailLacks
}

// Clear removes all nodes matching pred. Returns number removed.
func (m *NodeMap) Clear(pred func(Node) bool) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	before := len(m.nodes)
	m.nodes = slices.DeleteFunc(m.nodes, pred)
	return before - len(m.nodes)
}

// AuditExpired deletes all expired nodes and returns them.
func (m *NodeMap) AuditExpired() []Node {
	m.mu.Lock()
	defer m.mu.Unlock()
	var expired []Node
	kept := m.nodes[:0]
	for _, n := range m.nodes {
		if n.HasExpired() {
			expired = append(expired, n)
		} else {
			kept = append(kept, n)
		}
	}
	m.nodes = kept
	return expired
}

// PermissionHolder is the shared base of User and Group.
type PermissionHolder struct {
	lp             *LuckPerms
	holderType     HolderType
	idName         string // lowercase group name or uuid string
	normalNodes    NodeMap
	transientNodes NodeMap
	cached         *CachedData
}

func (h *PermissionHolder) init(lp *LuckPerms, typ HolderType, idName string) {
	h.lp = lp
	h.holderType = typ
	h.idName = idName
	h.cached = newCachedData(lp, h)
}

// Type returns HolderUser or HolderGroup.
func (h *PermissionHolder) Type() HolderType { return h.holderType }

// Identifier returns the canonical identifier string (UUID string for users, lowercase name for groups).
func (h *PermissionHolder) Identifier() string { return h.idName }

// Origin returns the NodeOrigin describing this holder.
func (h *PermissionHolder) Origin() NodeOrigin {
	return NodeOrigin{Type: h.holderType, Name: h.idName}
}

// Data returns the NodeMap for DataNormal or DataTransient.
func (h *PermissionHolder) Data(dt DataType) *NodeMap {
	if dt == DataTransient {
		return &h.transientNodes
	}
	return &h.normalNodes
}

// NormalNodes returns a snapshot of all persistent nodes.
func (h *PermissionHolder) NormalNodes() []Node { return h.normalNodes.List() }

// TransientNodes returns a snapshot of all transient nodes.
func (h *PermissionHolder) TransientNodes() []Node { return h.transientNodes.List() }

// AllOwnNodes returns transient nodes followed by normal nodes.
func (h *PermissionHolder) AllOwnNodes() []Node {
	tr := h.transientNodes.List()
	nm := h.normalNodes.List()
	out := make([]Node, 0, len(tr)+len(nm))
	origin := h.Origin()
	for _, n := range tr {
		out = append(out, n.WithOrigin(origin))
	}
	for _, n := range nm {
		out = append(out, n.WithOrigin(origin))
	}
	return out
}

// SetNode adds a node to dt and invalidates caches.
func (h *PermissionHolder) SetNode(dt DataType, n Node) MutateResult {
	res := h.Data(dt).Add(n)
	if res.WasSuccessful() {
		h.InvalidateCache()
		h.lp.fireNodeAdd(h, dt, n)
	}
	return res
}

// SetNodeTemp adds a temporary node using strategy and invalidates caches.
func (h *PermissionHolder) SetNodeTemp(dt DataType, n Node, strategy TemporaryNodeMergeStrategy) (Node, MutateResult) {
	stored, res := h.Data(dt).AddTemp(n, strategy)
	if res.WasSuccessful() {
		h.InvalidateCache()
		h.lp.fireNodeAdd(h, dt, stored)
	}
	return stored, res
}

// UnsetNode removes nodes matching n under EqualityIgnoreExpiryAndValue (or exact if permanent/temp matches).
func (h *PermissionHolder) UnsetNode(dt DataType, n Node) MutateResult {
	res := h.Data(dt).Remove(n, EqualityIgnoreValue)
	if res.WasSuccessful() {
		h.InvalidateCache()
		h.lp.fireNodeRemove(h, dt, n)
	}
	return res
}

// UnsetNodeMatching removes nodes matching n under eq.
func (h *PermissionHolder) UnsetNodeMatching(dt DataType, n Node, eq NodeEquality) MutateResult {
	res := h.Data(dt).Remove(n, eq)
	if res.WasSuccessful() {
		h.InvalidateCache()
		h.lp.fireNodeRemove(h, dt, n)
	}
	return res
}

// UnsetNodeTemp subtracts duration from or removes a temporary node.
func (h *PermissionHolder) UnsetNodeTemp(dt DataType, n Node, subtract time.Duration) (*Node, bool, MutateResult) {
	updated, removed, res := h.Data(dt).RemoveTemp(n, subtract)
	if res.WasSuccessful() {
		h.InvalidateCache()
		h.lp.fireNodeRemove(h, dt, n)
	}
	return updated, removed, res
}

// ClearNodes removes nodes matching pred and invalidates caches.
func (h *PermissionHolder) ClearNodes(dt DataType, pred func(Node) bool) int {
	n := h.Data(dt).Clear(pred)
	if n > 0 {
		h.InvalidateCache()
	}
	return n
}

// AuditTemporaryNodes removes expired nodes from both transient and normal maps.
func (h *PermissionHolder) AuditTemporaryNodes() bool {
	e1 := h.transientNodes.AuditExpired()
	e2 := h.normalNodes.AuditExpired()
	if len(e1) > 0 || len(e2) > 0 {
		h.InvalidateCache()
		return true
	}
	return false
}

// InvalidateCache clears this holder's cached permissions and meta.
// If this holder is a Group, all loaded users and groups are invalidated too.
func (h *PermissionHolder) InvalidateCache() {
	if h.cached != nil {
		h.cached.Invalidate()
	}
	if h.holderType == HolderGroup && h.lp != nil {
		h.lp.InvalidateAllCaches()
	}
	if h.lp != nil {
		h.lp.onHolderCacheInvalidated(h)
	}
}

// CachedData returns this holder's CachedData manager.
func (h *PermissionHolder) CachedData() *CachedData { return h.cached }

// OwnNodes returns this holder's nodes that apply under qo, sorted with contextual/temp priority.
func (h *PermissionHolder) OwnNodes(qo QueryOptions) []Node {
	all := h.AllOwnNodes()
	var out []Node
	for _, n := range all {
		if qo.NodeApplies(n) {
			out = append(out, n)
		}
	}
	slices.SortStableFunc(out, CompareNodes)
	return out
}

// OwnInheritanceNodes returns active positive inheritance nodes under qo.
func (h *PermissionHolder) OwnInheritanceNodes(qo QueryOptions) []Node {
	all := h.AllOwnNodes()
	var out []Node
	for _, n := range all {
		if n.Type == NodeTypeInheritance && n.Value && qo.NodeApplies(n) {
			out = append(out, n)
		}
	}
	slices.SortStableFunc(out, CompareNodes)
	return out
}

// WeightResult computes this holder's weight from weight nodes (and config group-weight overrides for groups).
func (h *PermissionHolder) WeightResult() IntegerResult {
	var best IntegerResult
	for _, n := range h.AllOwnNodes() {
		if n.Type == NodeTypeWeight && n.Value && !n.HasExpired() {
			if !best.Has || n.Weight > best.Value {
				nc := n
				best = IntegerResult{Value: n.Weight, Has: true, Node: &nc}
			}
		}
	}
	if !best.Has && h.holderType == HolderGroup && h.lp != nil {
		if w, ok := h.lp.Config().GroupWeights[strings.ToLower(h.idName)]; ok {
			best = IntegerResult{Value: w, Has: true}
		}
	}
	return best
}

// Weight returns the holder's weight and whether one is defined.
func (h *PermissionHolder) Weight() (int, bool) {
	r := h.WeightResult()
	return r.Value, r.Has
}

// DisplayName returns the holder's displayname node for qo (if any).
func (h *PermissionHolder) DisplayName(qo QueryOptions) string {
	for _, n := range h.OwnNodes(qo) {
		if n.Type == NodeTypeDisplayName && n.Value {
			return n.DisplayName
		}
	}
	return ""
}

// ResolveInheritedNodes traverses the inheritance graph and collects all applicable nodes in priority order.
func (h *PermissionHolder) ResolveInheritedNodes(qo QueryOptions) []Node {
	if !qo.Flags.ResolveInheritance {
		return h.OwnNodes(qo)
	}
	holders := h.lp.traverseInheritance(h, qo)
	var out []Node
	for _, holder := range holders {
		out = append(out, holder.OwnNodes(qo)...)
	}
	return out
}

// ExportPermissions builds the flattened lowercase permission map (including shorthand expansion if configured).
func (h *PermissionHolder) ExportPermissions(qo QueryOptions, resolveShorthand bool) map[string]Node {
	entries := h.ResolveInheritedNodes(qo)
	out := make(map[string]Node, len(entries))
	for _, n := range entries {
		k := strings.ToLower(n.Key)
		if _, exists := out[k]; !exists {
			out[k] = n
		}
	}
	if resolveShorthand {
		for _, n := range entries {
			if expanded, err := ExpandShorthand(n.Key); err == nil && len(expanded) > 0 {
				for _, s := range expanded {
					k := strings.ToLower(s)
					if _, exists := out[k]; !exists {
						out[k] = n
					}
				}
			}
		}
	}
	return out
}

// User represents a player's permission data.
type User struct {
	PermissionHolder

	mu           sync.RWMutex
	uuid         plugin.UUID
	username     string
	primaryGroup string // stored primary group
}

func newUser(lp *LuckPerms, id plugin.UUID, username string) *User {
	u := &User{
		uuid:         id,
		username:     username,
		primaryGroup: DefaultGroupName,
	}
	u.init(lp, HolderUser, id.String())
	return u
}

// UUID returns the user's unique ID.
func (u *User) UUID() plugin.UUID { return u.uuid }

// Username returns the user's last known username (may be empty).
func (u *User) Username() string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.username
}

// SetUsername updates the user's username.
func (u *User) SetUsername(name string) {
	if name == "" {
		return
	}
	u.mu.Lock()
	u.username = name
	u.mu.Unlock()
}

// FriendlyName returns Username if known, else UUID string.
func (u *User) FriendlyName() string {
	if name := u.Username(); name != "" {
		return name
	}
	return u.uuid.String()
}

// StoredPrimaryGroup returns the stored primary group name.
func (u *User) StoredPrimaryGroup() string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if u.primaryGroup == "" {
		return DefaultGroupName
	}
	return u.primaryGroup
}

// SetStoredPrimaryGroup sets the stored primary group name.
func (u *User) SetStoredPrimaryGroup(group string) {
	u.mu.Lock()
	if group == "" {
		u.primaryGroup = DefaultGroupName
	} else {
		u.primaryGroup = strings.ToLower(group)
	}
	u.mu.Unlock()
	u.InvalidateCache()
}

// PrimaryGroup computes the user's effective primary group according to config `primary-group-calculation`.
func (u *User) PrimaryGroup(qo QueryOptions) string {
	cfg := u.lp.Config()
	switch cfg.PrimaryGroupCalc {
	case PrimaryGroupStored:
		return u.StoredPrimaryGroup()
	case PrimaryGroupAllParentsByWeight:
		holders := u.lp.traverseInheritanceWithSort(&u.PermissionHolder, qo, true)
		for _, h := range holders {
			if h.Type() == HolderGroup {
				return h.Identifier()
			}
		}
		return u.StoredPrimaryGroup()
	default: // PrimaryGroupParentsByWeight
		var bestGroup *Group
		bestWeight := 0
		for _, n := range u.OwnInheritanceNodes(qo) {
			g := u.lp.Groups.GetIfLoaded(n.GroupName)
			if g == nil {
				continue
			}
			w, _ := g.Weight()
			if bestGroup == nil || w > bestWeight {
				bestGroup = g
				bestWeight = w
			}
		}
		if bestGroup != nil {
			return bestGroup.Name()
		}
		return u.StoredPrimaryGroup()
	}
}

// GiveDefaultIfNeeded ensures the user inherits the default group if they have no global inheritance nodes.
func (u *User) GiveDefaultIfNeeded() bool {
	hasGroup := false
	for _, n := range u.normalNodes.List() {
		if n.Type == NodeTypeInheritance && n.Value && n.Contexts.IsEmpty() && !n.HasExpiry() {
			hasGroup = true
			break
		}
	}
	if !hasGroup {
		u.normalNodes.Add(NewInheritanceNode(DefaultGroupName))
		return true
	}
	return false
}

// IsNonDefaultUser reports whether this user has data worth persisting to disk
// (more than just group.default and primary-group=default).
func (u *User) IsNonDefaultUser() bool {
	if !strings.EqualFold(u.StoredPrimaryGroup(), DefaultGroupName) {
		return true
	}
	nodes := u.normalNodes.List()
	if len(nodes) == 0 {
		return false
	}
	if len(nodes) == 1 {
		n := nodes[0]
		if n.Type == NodeTypeInheritance && n.GroupName == DefaultGroupName && n.Value && !n.HasExpiry() && n.Contexts.IsEmpty() {
			return false
		}
	}
	return true
}

// Group represents a permission group.
type Group struct {
	PermissionHolder
}

func newGroup(lp *LuckPerms, name string) *Group {
	g := &Group{}
	g.init(lp, HolderGroup, strings.ToLower(name))
	return g
}

// Name returns the lowercase group name.
func (g *Group) Name() string { return g.idName }

// FriendlyDisplayName returns the group's displayname if set, else its name.
func (g *Group) FriendlyDisplayName(qo QueryOptions) string {
	if dn := g.DisplayName(qo); dn != "" {
		return dn
	}
	return g.Name()
}

// Track is an ordered progression ladder of group names.
type Track struct {
	lp     *LuckPerms
	name   string
	mu     sync.RWMutex
	groups []string
}

func newTrack(lp *LuckPerms, name string) *Track {
	return &Track{lp: lp, name: strings.ToLower(name)}
}

// Name returns the lowercase track name.
func (t *Track) Name() string { return t.name }

// Groups returns a copy of the ordered group names on this track.
func (t *Track) Groups() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return slices.Clone(t.groups)
}

// SetGroups replaces the track's groups.
func (t *Track) SetGroups(groups []string) {
	t.mu.Lock()
	t.groups = make([]string, 0, len(groups))
	for _, g := range groups {
		gl := strings.ToLower(g)
		if !slices.Contains(t.groups, gl) {
			t.groups = append(t.groups, gl)
		}
	}
	t.mu.Unlock()
}

// Size returns the number of groups on this track.
func (t *Track) Size() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.groups)
}

// ContainsGroup reports whether group is on this track.
func (t *Track) ContainsGroup(group string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return slices.Contains(t.groups, strings.ToLower(group))
}

// AppendGroup adds group to the end of the track.
func (t *Track) AppendGroup(group string) MutateResult {
	gl := strings.ToLower(group)
	t.mu.Lock()
	defer t.mu.Unlock()
	if slices.Contains(t.groups, gl) {
		return MutateFailAlreadyHas
	}
	t.groups = append(t.groups, gl)
	return MutateSuccess
}

// InsertGroup inserts group at 0-based position pos.
func (t *Track) InsertGroup(group string, pos int) (MutateResult, bool) {
	gl := strings.ToLower(group)
	t.mu.Lock()
	defer t.mu.Unlock()
	if pos < 0 || pos > len(t.groups) {
		return MutateFail, false
	}
	if slices.Contains(t.groups, gl) {
		return MutateFailAlreadyHas, true
	}
	t.groups = slices.Insert(t.groups, pos, gl)
	return MutateSuccess, true
}

// RemoveGroup deletes group from the track.
func (t *Track) RemoveGroup(group string) MutateResult {
	gl := strings.ToLower(group)
	t.mu.Lock()
	defer t.mu.Unlock()
	idx := slices.Index(t.groups, gl)
	if idx == -1 {
		return MutateFailLacks
	}
	t.groups = slices.Delete(t.groups, idx, idx+1)
	return MutateSuccess
}

// ClearGroups empties the track.
func (t *Track) ClearGroups() {
	t.mu.Lock()
	t.groups = nil
	t.mu.Unlock()
}

// PromotionStatus enumerates promotion/demotion outcomes.
type PromotionStatus int

const (
	PromoSuccess PromotionStatus = iota
	PromoAddedToFirst
	PromoRemovedFromFirst
	PromoEndOfTrack
	PromoNotOnTrack
	PromoAmbiguousCall
	PromoMalformedTrack
	PromoEmptyTrack
	PromoPermissionDenied
)

// PromotionResult captures the outcome of Track.Promote / Track.Demote.
type PromotionResult struct {
	Status    PromotionStatus
	GroupFrom string
	GroupTo   string
	Missing   string // missing group name for PromoMalformedTrack
}

// Promote advances user one step up track in exact contexts.
func (t *Track) Promote(user *User, contexts ContextSet, permCheck func(nextGroup string) bool, addToFirst bool) PromotionResult {
	groups := t.Groups()
	if len(groups) <= 1 {
		return PromotionResult{Status: PromoEmptyTrack}
	}

	var matched []Node
	for _, n := range user.NormalNodes() {
		if n.Type == NodeTypeInheritance && n.Value && !n.HasExpired() && n.Contexts.Equal(contexts) && slices.Contains(groups, n.GroupName) {
			matched = append(matched, n)
		}
	}

	if len(matched) == 0 {
		if !addToFirst {
			return PromotionResult{Status: PromoAddedToFirst}
		}
		first := groups[0]
		if t.lp.Groups.GetIfLoaded(first) == nil {
			return PromotionResult{Status: PromoMalformedTrack, Missing: first}
		}
		if permCheck != nil && !permCheck(first) {
			return PromotionResult{Status: PromoPermissionDenied, GroupTo: first}
		}
		user.SetNode(DataNormal, NewInheritanceNode(first).WithContexts(contexts))
		t.lp.fireUserPromote(user, t, "", first)
		return PromotionResult{Status: PromoAddedToFirst, GroupTo: first}
	}

	if len(matched) != 1 {
		return PromotionResult{Status: PromoAmbiguousCall}
	}

	oldNode := matched[0]
	oldName := oldNode.GroupName
	idx := slices.Index(groups, oldName)
	if idx == len(groups)-1 {
		return PromotionResult{Status: PromoEndOfTrack, GroupFrom: oldName}
	}
	nextName := groups[idx+1]
	if t.lp.Groups.GetIfLoaded(nextName) == nil {
		return PromotionResult{Status: PromoMalformedTrack, Missing: nextName}
	}
	if permCheck != nil && !permCheck(nextName) {
		return PromotionResult{Status: PromoPermissionDenied, GroupFrom: oldName, GroupTo: nextName}
	}

	user.UnsetNodeMatching(DataNormal, oldNode, EqualityExact)
	newNode := NewInheritanceNode(nextName).WithContexts(oldNode.Contexts).WithExpiry(oldNode.Expiry)
	user.SetNode(DataNormal, newNode)
	if contexts.IsEmpty() && strings.EqualFold(user.StoredPrimaryGroup(), oldName) {
		user.SetStoredPrimaryGroup(nextName)
	}
	t.lp.fireUserPromote(user, t, oldName, nextName)
	return PromotionResult{Status: PromoSuccess, GroupFrom: oldName, GroupTo: nextName}
}

// Demote moves user one step down track in exact contexts.
func (t *Track) Demote(user *User, contexts ContextSet, permCheck func(oldGroup string) bool, removeFromFirst bool) PromotionResult {
	groups := t.Groups()
	if len(groups) <= 1 {
		return PromotionResult{Status: PromoEmptyTrack}
	}

	var matched []Node
	for _, n := range user.NormalNodes() {
		if n.Type == NodeTypeInheritance && n.Value && !n.HasExpired() && n.Contexts.Equal(contexts) && slices.Contains(groups, n.GroupName) {
			matched = append(matched, n)
		}
	}

	if len(matched) == 0 {
		return PromotionResult{Status: PromoNotOnTrack}
	}
	if len(matched) != 1 {
		return PromotionResult{Status: PromoAmbiguousCall}
	}

	oldNode := matched[0]
	oldName := oldNode.GroupName
	if permCheck != nil && !permCheck(oldName) {
		return PromotionResult{Status: PromoPermissionDenied, GroupFrom: oldName}
	}
	idx := slices.Index(groups, oldName)
	if idx == 0 {
		if !removeFromFirst {
			return PromotionResult{Status: PromoRemovedFromFirst}
		}
		user.UnsetNodeMatching(DataNormal, oldNode, EqualityExact)
		user.GiveDefaultIfNeeded()
		t.lp.fireUserDemote(user, t, oldName, "")
		return PromotionResult{Status: PromoRemovedFromFirst, GroupFrom: oldName}
	}
	prevName := groups[idx-1]
	if t.lp.Groups.GetIfLoaded(prevName) == nil {
		return PromotionResult{Status: PromoMalformedTrack, Missing: prevName}
	}
	user.UnsetNodeMatching(DataNormal, oldNode, EqualityExact)
	newNode := NewInheritanceNode(prevName).WithContexts(oldNode.Contexts).WithExpiry(oldNode.Expiry)
	user.SetNode(DataNormal, newNode)
	if contexts.IsEmpty() && strings.EqualFold(user.StoredPrimaryGroup(), oldName) {
		user.SetStoredPrimaryGroup(prevName)
	}
	t.lp.fireUserDemote(user, t, oldName, prevName)
	return PromotionResult{Status: PromoSuccess, GroupFrom: oldName, GroupTo: prevName}
}

// --- Managers ---

// UserManager manages loaded User instances.
type UserManager struct {
	lp    *LuckPerms
	mu    sync.RWMutex
	users map[plugin.UUID]*User
}

func newUserManager(lp *LuckPerms) *UserManager {
	return &UserManager{lp: lp, users: make(map[plugin.UUID]*User)}
}

// GetIfLoaded returns the loaded User or nil.
func (m *UserManager) GetIfLoaded(id plugin.UUID) *User {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.users[id]
}

// GetOrMake returns an existing loaded User or creates a new one.
func (m *UserManager) GetOrMake(id plugin.UUID, username string) *User {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[id]; ok {
		if username != "" {
			u.SetUsername(username)
		}
		return u
	}
	u := newUser(m.lp, id, username)
	m.users[id] = u
	return u
}

// Unload removes a user from memory.
func (m *UserManager) Unload(id plugin.UUID) {
	m.mu.Lock()
	delete(m.users, id)
	m.mu.Unlock()
}

// Loaded returns a snapshot of all loaded users.
func (m *UserManager) Loaded() []*User {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*User, 0, len(m.users))
	for _, u := range m.users {
		out = append(out, u)
	}
	return out
}

// GroupManager manages loaded Group instances.
type GroupManager struct {
	lp     *LuckPerms
	mu     sync.RWMutex
	groups map[string]*Group
}

func newGroupManager(lp *LuckPerms) *GroupManager {
	return &GroupManager{lp: lp, groups: make(map[string]*Group)}
}

// GetIfLoaded returns the group or nil.
func (m *GroupManager) GetIfLoaded(name string) *Group {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.groups[strings.ToLower(name)]
}

// GetOrMake returns the group or creates it in memory.
func (m *GroupManager) GetOrMake(name string) *Group {
	key := strings.ToLower(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.groups[key]; ok {
		return g
	}
	g := newGroup(m.lp, key)
	m.groups[key] = g
	return g
}

// Unload removes a group from memory.
func (m *GroupManager) Unload(name string) {
	m.mu.Lock()
	delete(m.groups, strings.ToLower(name))
	m.mu.Unlock()
}

// All returns all loaded groups sorted by descending weight, then name.
func (m *GroupManager) All() []*Group {
	m.mu.RLock()
	out := make([]*Group, 0, len(m.groups))
	for _, g := range m.groups {
		out = append(out, g)
	}
	m.mu.RUnlock()
	slices.SortFunc(out, func(a, b *Group) int {
		wa, _ := a.Weight()
		wb, _ := b.Weight()
		if c := cmp.Compare(wb, wa); c != 0 {
			return c
		}
		return cmp.Compare(a.Name(), b.Name())
	})
	return out
}

// Names returns sorted group names.
func (m *GroupManager) Names() []string {
	all := m.All()
	names := make([]string, len(all))
	for i, g := range all {
		names[i] = g.Name()
	}
	return names
}

// TrackManager manages loaded Track instances.
type TrackManager struct {
	lp     *LuckPerms
	mu     sync.RWMutex
	tracks map[string]*Track
}

func newTrackManager(lp *LuckPerms) *TrackManager {
	return &TrackManager{lp: lp, tracks: make(map[string]*Track)}
}

// GetIfLoaded returns the track or nil.
func (m *TrackManager) GetIfLoaded(name string) *Track {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tracks[strings.ToLower(name)]
}

// GetOrMake returns the track or creates it in memory.
func (m *TrackManager) GetOrMake(name string) *Track {
	key := strings.ToLower(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tracks[key]; ok {
		return t
	}
	t := newTrack(m.lp, key)
	m.tracks[key] = t
	return t
}

// Unload removes a track from memory.
func (m *TrackManager) Unload(name string) {
	m.mu.Lock()
	delete(m.tracks, strings.ToLower(name))
	m.mu.Unlock()
}

// All returns all loaded tracks sorted by name.
func (m *TrackManager) All() []*Track {
	m.mu.RLock()
	out := make([]*Track, 0, len(m.tracks))
	for _, t := range m.tracks {
		out = append(out, t)
	}
	m.mu.RUnlock()
	slices.SortFunc(out, func(a, b *Track) int { return cmp.Compare(a.Name(), b.Name()) })
	return out
}

// Names returns sorted track names.
func (m *TrackManager) Names() []string {
	all := m.All()
	names := make([]string, len(all))
	for i, t := range all {
		names[i] = t.Name()
	}
	return names
}

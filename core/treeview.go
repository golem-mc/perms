package core

import (
	"slices"
	"strings"
	"sync"
)

// TreeNode is one node in the hierarchical permission tree.
type TreeNode struct {
	Children map[string]*TreeNode
}

// PermissionRegistry tracks all known permission nodes for tab completion and `/lp tree`.
type PermissionRegistry struct {
	mu   sync.RWMutex
	root *TreeNode
	all  map[string]struct{}
}

func newPermissionRegistry() *PermissionRegistry {
	return &PermissionRegistry{
		root: &TreeNode{Children: make(map[string]*TreeNode)},
		all:  make(map[string]struct{}),
	}
}

// Offer records a permission node in the tree and flat set.
func (r *PermissionRegistry) Offer(perm string) {
	perm = strings.ToLower(strings.TrimSpace(perm))
	if perm == "" {
		return
	}
	r.mu.RLock()
	_, exists := r.all[perm]
	r.mu.RUnlock()
	if exists {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.all[perm]; exists {
		return
	}
	r.all[perm] = struct{}{}
	parts := strings.Split(perm, ".")
	cur := r.root
	for _, part := range parts {
		if part == "" {
			continue
		}
		if cur.Children == nil {
			cur.Children = make(map[string]*TreeNode)
		}
		next := cur.Children[part]
		if next == nil {
			next = &TreeNode{}
			cur.Children[part] = next
		}
		cur = next
	}
}

// All returns all known permissions sorted alphabetically.
func (r *PermissionRegistry) All() []string {
	r.mu.RLock()
	out := make([]string, 0, len(r.all))
	for k := range r.all {
		out = append(out, k)
	}
	r.mu.RUnlock()
	slices.Sort(out)
	return out
}

// TreeLine is one rendered line of a permission tree.
type TreeLine struct {
	Indent     int
	Label      string
	FullNode   string
	CheckState Tristate // populated when checking against a user
	HasCheck   bool
}

// BuildTree renders the permission tree under scope ("" or "." for root).
// If checkFn is non-nil, each node is evaluated against it.
func (r *PermissionRegistry) BuildTree(scope string, maxDepth int, checkFn func(string) Tristate) []TreeLine {
	r.mu.RLock()
	defer r.mu.RUnlock()

	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "." {
		scope = ""
	}
	cur := r.root
	prefix := ""
	if scope != "" {
		for _, part := range strings.Split(scope, ".") {
			if part == "" {
				continue
			}
			if cur.Children == nil {
				return nil
			}
			next := cur.Children[part]
			if next == nil {
				return nil
			}
			cur = next
		}
		prefix = scope
	}

	var lines []TreeLine
	var walk func(n *TreeNode, path string, depth int)
	walk = func(n *TreeNode, path string, depth int) {
		if maxDepth > 0 && depth > maxDepth {
			return
		}
		if len(n.Children) == 0 {
			return
		}
		keys := make([]string, 0, len(n.Children))
		for k := range n.Children {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			full := k
			if path != "" {
				full = path + "." + k
			}
			line := TreeLine{Indent: depth, Label: k, FullNode: full}
			if checkFn != nil {
				line.CheckState = checkFn(full)
				line.HasCheck = true
			}
			lines = append(lines, line)
			walk(n.Children[k], full, depth+1)
		}
	}
	walk(cur, prefix, 0)
	return lines
}

package core

import (
	"cmp"
	"slices"
	"strings"
)

// traverseInheritance traverses the inheritance graph from start using configured algorithm and post-sort.
func (lp *LuckPerms) traverseInheritance(start *PermissionHolder, qo QueryOptions) []*PermissionHolder {
	cfg := lp.Config()
	return lp.traverseInheritanceWithSort(start, qo, cfg.PostTraversalSort)
}

func (lp *LuckPerms) traverseInheritanceWithSort(start *PermissionHolder, qo QueryOptions, postSort bool) []*PermissionHolder {
	cfg := lp.Config()
	originPrimary := ""
	if start.Type() == HolderUser {
		if u := lp.Users.GetIfLoaded(parseUUIDSilent(start.Identifier())); u != nil {
			originPrimary = u.StoredPrimaryGroup()
		}
	}

	compareHolders := func(a, b *PermissionHolder) int {
		// Users always come before groups.
		if a.Type() != b.Type() {
			if a.Type() == HolderUser {
				return -1
			}
			return 1
		}
		if a.Type() == HolderUser {
			return 0
		}
		// Higher weight comes first.
		wa, _ := a.Weight()
		wb, _ := b.Weight()
		if c := cmp.Compare(wb, wa); c != 0 {
			return c
		}
		// Primary group tiebreaker.
		if originPrimary != "" {
			aPrim := strings.EqualFold(a.Identifier(), originPrimary)
			bPrim := strings.EqualFold(b.Identifier(), originPrimary)
			if aPrim != bPrim {
				if aPrim {
					return -1
				}
				return 1
			}
		}
		return 0
	}

	successors := func(h *PermissionHolder) []*PermissionHolder {
		seen := make(map[string]struct{})
		var succ []*PermissionHolder
		for _, n := range h.OwnInheritanceNodes(qo) {
			if _, ok := seen[n.GroupName]; ok {
				continue
			}
			if g := lp.Groups.GetIfLoaded(n.GroupName); g != nil {
				seen[n.GroupName] = struct{}{}
				succ = append(succ, &g.PermissionHolder)
			}
		}
		slices.SortStableFunc(succ, compareHolders)
		return succ
	}

	var result []*PermissionHolder
	switch cfg.InheritanceTraversal {
	case TraversalBreadthFirst:
		result = traverseBFS(start, successors)
	case TraversalDepthFirstPostOrder:
		result = traverseDFS(start, successors, false)
	default: // TraversalDepthFirstPreOrder
		result = traverseDFS(start, successors, true)
	}

	if postSort {
		slices.SortStableFunc(result, compareHolders)
	}
	return result
}

func traverseBFS(root *PermissionHolder, successors func(*PermissionHolder) []*PermissionHolder) []*PermissionHolder {
	visited := map[*PermissionHolder]struct{}{root: {}}
	queue := []*PermissionHolder{root}
	var out []*PermissionHolder
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		out = append(out, cur)
		for _, next := range successors(cur) {
			if _, seen := visited[next]; !seen {
				visited[next] = struct{}{}
				queue = append(queue, next)
			}
		}
	}
	return out
}

func traverseDFS(root *PermissionHolder, successors func(*PermissionHolder) []*PermissionHolder, preOrder bool) []*PermissionHolder {
	type frame struct {
		node *PermissionHolder
		succ []*PermissionHolder
		idx  int
	}
	visited := make(map[*PermissionHolder]struct{})
	stack := []*frame{{node: root, succ: successors(root)}}
	var out []*PermissionHolder
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		_, alreadyVisited := visited[top.node]
		firstVisit := !alreadyVisited
		if firstVisit {
			visited[top.node] = struct{}{}
			if preOrder {
				out = append(out, top.node)
			}
		}
		if top.idx >= len(top.succ) {
			stack = stack[:len(stack)-1]
			if !preOrder {
				out = append(out, top.node)
			}
			continue
		}
		next := top.succ[top.idx]
		top.idx++
		if _, seen := visited[next]; !seen {
			stack = append(stack, &frame{node: next, succ: successors(next)})
		}
	}
	return out
}

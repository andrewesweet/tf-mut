package discovery

import (
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// The attribute-level reference graph (M3a.1, spec review C3).
//
// Nodes are resource and data attributes, locals, variables, outputs, module
// inputs and outputs, and condition expressions; edges come from hclsyntax
// expression traversal plus module wiring. Every address is in the canonical
// model of address.go, and both adapters fail closed: a mutation site that
// does not map to a graph node falls back to the whole-payload unknown rule
// for that mutant, and a payload unknown that does not map into the graph is
// treated as in-cone.
//
// The graph is deliberately conservative in three recorded ways, each in the
// direction that over-reports reach and so can never license a false proof:
// instance keys are wildcarded, a module called twice is one node set, and a
// reference to an attribute the configuration never assigns resolves to the
// whole block.

// nodeID identifies one graph node: a module of the closure and a canonical
// address within it.
type nodeID struct {
	// module is the closure-relative module directory.
	module string
	// address is the canonical in-module address.
	address string
}

// Graph is the attribute-level reference graph of one configuration.
type Graph struct {
	// nodes is the set of declared addresses.
	nodes map[nodeID]bool
	// dependents lists, per node, the nodes whose expressions observe it.
	dependents map[nodeID][]nodeID
	// owners maps an attribute node to its enclosing resource or data block
	// node, which is what the same-resource attribute union is computed from.
	owners map[nodeID]nodeID
	// moduleByPath resolves a module-call path from the root to the
	// closure-relative directory of the module it instantiates.
	moduleByPath map[string]string
}

// Cone is the forward cone of one mutation site: the closure from the mutated
// node together with every attribute of any resource the closure touches.
type Cone struct {
	graph    *Graph
	members  map[nodeID]bool
	touched  map[nodeID]bool
	moduleID string
}

// BuildGraph builds the reference graph once, from the already-parsed ASTs the
// discovery inventories were built from. No user invocation of Terraform is
// involved: the supplemental `terraform graph` comparison lives in the test
// suite only.
func (c Configuration) BuildGraph() *Graph {
	graph := &Graph{
		nodes:        map[nodeID]bool{},
		dependents:   map[nodeID][]nodeID{},
		owners:       map[nodeID]nodeID{},
		moduleByPath: map[string]string{},
	}

	relByDir := map[string]string{}
	for _, module := range c.Modules {
		relByDir[module.Dir] = module.Rel
	}

	graph.mapModulePaths(c, relByDir)

	// Two passes: every module's nodes exist before any edge resolves, so a
	// parent's reference to a child output — or a call's wiring into a child
	// variable — never depends on build order.
	for _, connect := range []bool{false, true} {
		for _, module := range c.Modules {
			builder := &graphBuilder{graph: graph, module: module, relByDir: relByDir, connect: connect}
			builder.build()
		}
	}

	return graph
}

func mustRel(relByDir map[string]string, dir string) string {
	if rel, ok := relByDir[dir]; ok {
		return rel
	}

	return ""
}

// graphBuilder accumulates one module's nodes and edges.
type graphBuilder struct {
	graph    *Graph
	module   Module
	relByDir map[string]string
	// connect selects the pass: false declares nodes, true draws edges.
	connect bool
}

func (b *graphBuilder) build() {
	for _, path := range b.module.Files {
		body, ok := b.module.Bodies[path]
		if !ok {
			continue
		}

		for _, block := range body.Blocks {
			b.buildBlock(block)
		}
	}
}

// buildBlock mirrors the mutation walker's address scheme exactly: the site an
// operator writes and the node the graph declares must be the same string, and
// the adapter sweep over every generation site is what holds the two walkers
// together.
func (b *graphBuilder) buildBlock(block *hclsyntax.Block) {
	switch block.Type {
	case resourceBlock, dataBlock:
		if len(block.Labels) != resourceLabelCount {
			return
		}

		address := block.Labels[0] + "." + block.Labels[1]
		if block.Type == dataBlock {
			address = dataBlock + "." + address
		}

		owner := b.declare(address)
		b.walkBody(block.Body, address, owner, owner)
	case outputBlock, moduleBlock, variableBlock, checkBlock:
		if len(block.Labels) != 1 {
			return
		}

		address := block.Type + "." + block.Labels[0]
		if block.Type == variableBlock {
			address = "var." + block.Labels[0]
		}

		owner := b.declare(address)
		b.walkBody(block.Body, address, owner, nodeID{})

		if block.Type == moduleBlock && b.connect {
			b.wireCall(block.Labels[0], owner)
		}
	case localsBlock:
		for name, attribute := range block.Body.Attributes {
			node := b.declare("local." + name)

			if b.connect {
				b.link(attribute.Expr, node)
			}
		}
	default:
	}
}

// checkBlock is the check block type, whose assertions observe resources.
const checkBlock = "check"

// walkBody declares an attribute node per attribute and recurses into nested
// blocks, appending the nested type and first label to the address the way the
// mutation walker does. Every attribute node depends towards its block node,
// so a mutation anywhere in a block reaches the block's readers.
func (b *graphBuilder) walkBody(body *hclsyntax.Body, address string, blockNode, owner nodeID) {
	for name, attribute := range body.Attributes {
		node := b.declare(address + "." + name)

		if owner != (nodeID{}) {
			b.graph.owners[node] = owner
		}

		if b.connect {
			// Bidirectional: a mutation at the attribute reaches the block's
			// readers, and a mutation of the whole block (a resource delete)
			// reaches every attribute's readers. Reader propagation is at
			// block granularity — coarse, in the conservative direction.
			b.graph.dependents[node] = append(b.graph.dependents[node], blockNode)
			b.graph.dependents[blockNode] = append(b.graph.dependents[blockNode], node)
			b.link(attribute.Expr, node)
		}
	}

	for _, nested := range body.Blocks {
		nestedAddress := address + "." + nested.Type
		if len(nested.Labels) > 0 {
			nestedAddress += "." + nested.Labels[0]
		}

		// The nested block is a node of its own: a block-removal operator's
		// site is the block path, not any one attribute in it.
		node := b.declare(nestedAddress)

		if owner != (nodeID{}) {
			b.graph.owners[node] = owner
		}

		if b.connect {
			b.graph.dependents[node] = append(b.graph.dependents[node], blockNode)
		}

		b.walkBody(nested.Body, nestedAddress, blockNode, owner)
	}
}

// declare adds a node for an in-module address.
func (b *graphBuilder) declare(address string) nodeID {
	node := nodeID{module: b.module.Rel, address: address}
	b.graph.nodes[node] = true

	return node
}

// wireCall connects a module call to the child module it instantiates: each
// input attribute feeds the child's variable, and each child output feeds the
// call's result object.
func (b *graphBuilder) wireCall(name string, callNode nodeID) {
	call, found := b.callByName(name)
	if !found || !call.Local {
		return
	}

	childRel, ok := b.relByDir[call.Dir]
	if !ok {
		return
	}

	for _, input := range call.Inputs {
		inputNode := nodeID{module: b.module.Rel, address: moduleBlock + "." + name + "." + input.Name}
		childVar := nodeID{module: childRel, address: "var." + input.Name}
		b.graph.dependents[inputNode] = append(b.graph.dependents[inputNode], childVar)
	}

	// Child outputs feed the call: a reader of module.<name>.<output> resolves
	// to the child's output node through resolve, and a whole-object read of
	// module.<name> reaches the outputs through this edge.
	for node := range b.graph.nodes {
		if node.module == childRel && strings.HasPrefix(node.address, outputBlock+".") {
			b.graph.dependents[node] = append(b.graph.dependents[node], callNode)
		}
	}
}

func (b *graphBuilder) callByName(name string) (ModuleCall, bool) {
	for _, call := range b.module.Calls {
		if call.Name == name {
			return call, true
		}
	}

	return ModuleCall{}, false
}

// link records an edge from every address the expression observes to the
// observing node.
func (b *graphBuilder) link(expr hclsyntax.Expression, reader nodeID) {
	for _, ref := range referencesOf(expr) {
		source, ok := b.resolveRef(ref.Address)
		if !ok {
			continue
		}

		b.graph.dependents[source] = append(b.graph.dependents[source], reader)
	}
}

// resolveRef resolves a referenced address to the node it observes, in this
// module or through a module call.
func (b *graphBuilder) resolveRef(address string) (nodeID, bool) {
	parsed := ParseAddr(address)
	if len(parsed.ModulePath) > 0 {
		return b.resolveCallRef(parsed)
	}

	if len(parsed.Parts) == 0 || unresolvableRoot(parsed.Parts[0]) {
		return nodeID{}, false
	}

	return b.graph.resolveNode(b.module.Rel, parsed.Parts)
}

// unresolvableRoot lists the traversal roots the graph draws no edge from:
// iteration and self scopes stay within their own block (whose attribute
// already edges to the block node), and path/terraform/run never name module
// configuration. Recorded conservatism: `self` inside a provisioner loses an
// edge, and provisioners have no payload projection to lose it for.
func unresolvableRoot(name string) bool {
	switch name {
	case "each", countKeyword, "self", "path", terraformBlock, "run", outputBlock, moduleBlock:
		return true
	default:
		return false
	}
}

// resolveCallRef resolves module.<call>.<output>... to the child's output.
func (b *graphBuilder) resolveCallRef(parsed Addr) (nodeID, bool) {
	if len(parsed.ModulePath) != 1 || len(parsed.Parts) == 0 {
		return nodeID{}, false
	}

	call, found := b.callByName(parsed.ModulePath[0])
	if !found || !call.Local {
		return nodeID{}, false
	}

	childRel, ok := b.relByDir[call.Dir]
	if !ok {
		return nodeID{}, false
	}

	node := nodeID{module: childRel, address: outputBlock + "." + parsed.Parts[0]}
	if b.graph.nodes[node] {
		return node, true
	}

	return nodeID{}, false
}

// SiteCone maps a mutation site into the graph and returns its forward cone.
// The second result is false where the site does not map, and the caller must
// fall back to the whole-payload unknown rule for that mutant — the adapter
// fails closed, never towards an empty cone.
func (g *Graph) SiteCone(moduleRel, site string) (Cone, bool) {
	// A site address is already module-local — module.<call>.<input> names a
	// node of the *calling* module — so the canonical form keeps its segments
	// in place and only sheds instance keys.
	start := nodeID{module: moduleRel, address: strings.Join(splitAddress(site), ".")}
	if !g.nodes[start] {
		return Cone{}, false
	}

	return g.forwardCone(start), true
}

// mapModulePaths records every module-call path from the root, so payload
// addresses like module.child.terraform_data.x can find their module.
func (g *Graph) mapModulePaths(c Configuration, relByDir map[string]string) {
	root, ok := c.ModuleByRel(mustRel(relByDir, c.ModuleDir))
	if !ok {
		return
	}

	type visit struct {
		module Module
		path   string
	}

	queue := []visit{{module: root, path: ""}}
	seen := map[string]bool{}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if seen[current.path] {
			continue
		}

		seen[current.path] = true
		g.moduleByPath[current.path] = current.module.Rel

		for _, call := range current.module.Calls {
			if !call.Local {
				continue
			}

			child, found := c.ModuleByRel(mustRel(relByDir, call.Dir))
			if !found {
				continue
			}

			queue = append(queue, visit{module: child, path: joinKey(current.path, call.Name)})
		}
	}
}

// resolveNode resolves canonical segments to the longest declared node,
// falling back towards the enclosing block. Wildcard segments end the search
// at the prefix before them, which over-matches — the conservative direction.
func (g *Graph) resolveNode(module string, parts []string) (nodeID, bool) {
	for index, segment := range parts {
		if segment == Wildcard {
			parts = parts[:index]

			break
		}
	}

	for length := len(parts); length > 0; length-- {
		node := nodeID{module: module, address: strings.Join(parts[:length], ".")}
		if g.nodes[node] {
			return node, true
		}
	}

	return nodeID{}, false
}

// forwardCone walks the dependents closure and unions in every attribute of
// any resource the closure touches.
func (g *Graph) forwardCone(start nodeID) Cone {
	members := map[nodeID]bool{}
	touched := map[nodeID]bool{}
	queue := []nodeID{start}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if members[current] {
			continue
		}

		members[current] = true

		if owner, ok := g.owners[current]; ok {
			touched[owner] = true
		}

		if isResourceBlock(current) {
			touched[current] = true
		}

		queue = append(queue, g.dependents[current]...)
	}

	return Cone{graph: g, members: members, touched: touched, moduleID: start.module}
}

// isResourceBlock reports whether a node is a resource or data block node,
// decided by address shape: two segments under a non-reserved root, or three
// under data.
func isResourceBlock(node nodeID) bool {
	parts := strings.Split(node.address, ".")

	if parts[0] == dataBlock {
		return len(parts) == dataAddressLength
	}

	return len(parts) == addressParts && !isReservedRoot(parts[0]) && parts[0] != checkBlock
}

// dataAddressLength is the segment count of a data block address.
const dataAddressLength = 3

// ContainsPayloadAddress reports whether a payload address lies in the cone.
// An address that does not map into the graph is treated as in-cone: the
// adapter fails closed, so an unmappable unknown blocks an equality claim
// rather than licensing one.
func (c Cone) ContainsPayloadAddress(address string) bool {
	if c.graph == nil {
		return true
	}

	parsed := ParseAddr(address)

	module, ok := c.graph.moduleByPath[strings.Join(parsed.ModulePath, ".")]
	if !ok {
		return true
	}

	node, ok := c.graph.resolveNode(module, parsed.Parts)
	if !ok {
		return true
	}

	return c.contains(node)
}

// ContainsResource reports whether the cone reaches a resource: directly, or
// through the same-resource attribute union.
func (c Cone) ContainsResource(moduleRel, address string) bool {
	node := nodeID{module: moduleRel, address: address}

	return c.contains(node)
}

func (c Cone) contains(node nodeID) bool {
	if c.members[node] {
		return true
	}

	if c.touched[node] {
		return true
	}

	if owner, ok := c.graph.owners[node]; ok && c.touched[owner] {
		return true
	}

	return false
}

package discovery

import (
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
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
	// sources lists, per node, the nodes its expression observes — pure
	// reference edges, without the block-union edges, so upstream questions
	// are answered from dataflow alone (M3a.3).
	sources map[nodeID][]nodeID
	// moduleByPath resolves a module-call path from the root to the
	// closure-relative directory of the module it instantiates.
	moduleByPath map[string]string
	// unbounded marks nodes whose influence the graph does not model edge by
	// edge — provider configuration, and module calls it could not wire — so
	// any cone touching one contains everything and licenses nothing.
	unbounded map[nodeID]bool
}

// Cone is the forward cone of one mutation site: the closure from the mutated
// node together with every attribute of any resource the closure touches.
type Cone struct {
	graph    *Graph
	members  map[nodeID]bool
	touched  map[nodeID]bool
	moduleID string
	// unbounded marks a cone that reached provider configuration, whose
	// influence the graph does not model edge by edge: an unbounded cone
	// contains everything and licenses nothing.
	unbounded bool
}

// UnmappedGraph is a graph in which nothing resolves: every adapter lookup
// fails closed, which is the whole-payload floor expressed as a graph.
//
// It is what the engine uses while unread JSON is present in the closure. The
// graph is built from `.tf` syntax alone, so JSON-declared references draw no
// edges; a cone computed over it would be missing edges without saying so,
// which is the false proof the M3 spec review's C3 prohibited.
func UnmappedGraph() *Graph {
	return &Graph{
		nodes:        map[nodeID]bool{},
		dependents:   map[nodeID][]nodeID{},
		owners:       map[nodeID]nodeID{},
		sources:      map[nodeID][]nodeID{},
		moduleByPath: map[string]string{},
		unbounded:    map[nodeID]bool{},
	}
}

// BuildGraph builds the reference graph once, from the already-parsed ASTs the
// discovery inventories were built from. No user invocation of Terraform is
// involved: the supplemental `terraform graph` comparison lives in the test
// suite only.
func (c Configuration) BuildGraph() *Graph {
	graph := UnmappedGraph()

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

	b.buildJSON()
}

// buildJSON declares the nodes and draws the edges a read JSON configuration
// file contributes (M4c). A JSON file is never a mutation site, so its blocks
// need no site-address parity with the mutation walker; what they need is the
// Terraform address a reader and a payload would use, so that a cone reaching
// one sees an observable and an expression referring to one draws an edge.
func (b *graphBuilder) buildJSON() {
	paths := make([]string, 0, len(b.module.JSONBodies))
	for path := range b.module.JSONBodies {
		paths = append(paths, path)
	}

	slices.Sort(paths)

	for _, path := range paths {
		content, _, diagnostics := b.module.JSONBodies[path].PartialContent(jsonConfigurationSchema)
		if diagnostics.HasErrors() {
			continue
		}

		for _, block := range content.Blocks {
			b.buildJSONBlock(block)
		}
	}
}

func (b *graphBuilder) buildJSONBlock(block *hcl.Block) {
	switch block.Type {
	case resourceBlock, dataBlock:
		address := block.Labels[0] + "." + block.Labels[1]
		if block.Type == dataBlock {
			address = dataBlock + "." + address
		}

		owner := b.declare(address)
		b.walkJSONBody(block.Body, address, owner, owner)
	case outputBlock, moduleBlock, variableBlock, checkBlock:
		address := block.Type + "." + block.Labels[0]
		if block.Type == variableBlock {
			address = "var." + block.Labels[0]
		}

		owner := b.declare(address)
		b.walkJSONBody(block.Body, address, owner, nodeID{})

		if block.Type == moduleBlock && b.connect {
			b.wireCall(block.Labels[0], owner)
		}
	case localsBlock:
		attributes, diagnostics := block.Body.JustAttributes()
		if diagnostics.HasErrors() {
			return
		}

		for name, attribute := range attributes {
			node := b.declare("local." + name)

			if b.connect {
				b.linkJSON(attribute.Expr, node)
			}
		}
	case providerBlock:
		owner := b.declare(providerBlock + "." + block.Labels[0])
		b.graph.unbounded[owner] = true
		b.walkJSONBody(block.Body, providerBlock+"."+block.Labels[0], owner, nodeID{})
	default:
	}
}

// walkJSONBody is walkBody over a JSON body. JSON draws no line between an
// attribute and a nested block, so everything the body carries is walked as an
// attribute: a nested block's own node would need a schema per nesting level,
// and its edges are already drawn by the traversals its expressions carry.
func (b *graphBuilder) walkJSONBody(body hcl.Body, address string, blockNode, owner nodeID) {
	attributes, diagnostics := body.JustAttributes()
	if diagnostics.HasErrors() {
		return
	}

	for name, attribute := range attributes {
		node := b.declare(address + "." + name)

		if owner != (nodeID{}) {
			b.graph.owners[node] = owner
		}

		if b.connect {
			b.graph.dependents[node] = append(b.graph.dependents[node], blockNode)
			b.graph.dependents[blockNode] = append(b.graph.dependents[blockNode], node)
			b.linkJSON(attribute.Expr, node)
		}
	}
}

// linkJSON records an edge from every address a JSON expression observes.
func (b *graphBuilder) linkJSON(expr hcl.Expression, reader nodeID) {
	for _, ref := range jsonRefs(expr) {
		source, ok := b.resolveRef(ref.Address)
		if !ok {
			continue
		}

		b.graph.dependents[source] = append(b.graph.dependents[source], reader)
		b.graph.sources[reader] = append(b.graph.sources[reader], source)
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
	case providerBlock:
		if len(block.Labels) != 1 {
			return
		}

		// Provider configuration reaches every resource the provider serves,
		// a reach the graph does not model edge by edge: the provider node
		// makes any cone that touches it unbounded, so no shortcut can turn
		// this into a proof (review of #44).
		owner := b.declare(providerBlock + "." + block.Labels[0])
		b.graph.unbounded[owner] = true
		b.walkBody(block.Body, providerBlock+"."+block.Labels[0], owner, nodeID{})
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

	// A JSON-declared call's inputs are not decoded, so the graph cannot model
	// its wiring edge by edge: the call is unbounded, and any cone touching it
	// contains everything and licenses nothing.
	if found && call.JSONDeclared {
		b.markCallUnbounded(callNode)

		return
	}

	if !found || !call.Local {
		// A remote call — or one discovery could not place — is wiring the
		// graph cannot model: a changed input can alter every resource and
		// output in the called module, so the call and its inputs make any
		// cone that touches them unbounded (re-review of #44).
		b.markCallUnbounded(callNode)

		return
	}

	childRel, ok := b.relByDir[call.Dir]
	if !ok {
		b.markCallUnbounded(callNode)

		return
	}

	for _, input := range call.Inputs {
		inputNode := nodeID{module: b.module.Rel, address: moduleBlock + "." + name + "." + input.Name}
		childVar := nodeID{module: childRel, address: "var." + input.Name}
		b.graph.dependents[inputNode] = append(b.graph.dependents[inputNode], childVar)
		b.graph.sources[childVar] = append(b.graph.sources[childVar], inputNode)
	}

	// Child outputs feed the call: a reader of module.<name>.<output> resolves
	// to the child's output node through resolve, and a whole-object read of
	// module.<name> reaches the outputs through this edge.
	for node := range b.graph.nodes {
		if node.module == childRel && strings.HasPrefix(node.address, outputBlock+".") {
			b.graph.dependents[node] = append(b.graph.dependents[node], callNode)
			b.graph.sources[callNode] = append(b.graph.sources[callNode], node)
		}
	}
}

// markCallUnbounded marks a module call node and every declared input node
// under it as unbounded.
func (b *graphBuilder) markCallUnbounded(callNode nodeID) {
	b.graph.unbounded[callNode] = true

	prefix := callNode.address + "."
	for node := range b.graph.nodes {
		if node.module == callNode.module && strings.HasPrefix(node.address, prefix) {
			b.graph.unbounded[node] = true
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
		b.graph.sources[reader] = append(b.graph.sources[reader], source)
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
	case "each", countKeyword, "self", "path", terraformBlock, "run", outputBlock:
		return true
	default:
		return false
	}
}

// resolveCallRef resolves module.<call>.<output>... to the child's output.
// Every shape it cannot express — a whole-object module read, a remote
// module's output, an output name discovery never saw — falls back to the
// call's own node, which remote wiring has already marked unbounded, so no
// reference is ever silently dropped (re-review of #44).
func (b *graphBuilder) resolveCallRef(parsed Addr) (nodeID, bool) {
	if len(parsed.ModulePath) == 0 {
		return nodeID{}, false
	}

	callNode := nodeID{module: b.module.Rel, address: moduleBlock + "." + parsed.ModulePath[0]}
	if !b.graph.nodes[callNode] {
		return nodeID{}, false
	}

	if len(parsed.ModulePath) != 1 || len(parsed.Parts) == 0 {
		return callNode, true
	}

	call, found := b.callByName(parsed.ModulePath[0])
	if !found || !call.Local {
		return callNode, true
	}

	childRel, ok := b.relByDir[call.Dir]
	if !ok {
		return callNode, true
	}

	node := nodeID{module: childRel, address: outputBlock + "." + parsed.Parts[0]}
	if b.graph.nodes[node] {
		return node, true
	}

	return callNode, true
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

// MultiplicityGuard reports whether a mutation site is in, under, or
// graph-upstream of a multiplicity expression, in which case the mutant must
// always execute (M3a.3, review C1). The walk follows pure reference edges
// from the multiplicity attribute towards its inputs; a site overlaps the
// closure when either address contains the other, so a whole-block mutation
// of the resource — which owns the expression — or of any upstream block is
// caught. A multiplicity node the graph cannot find fails closed to true.
func (g *Graph) MultiplicityGuard(moduleRel, metaAddress, siteModule, site string) bool {
	start := nodeID{module: moduleRel, address: strings.Join(splitAddress(metaAddress), ".")}
	if !g.nodes[start] {
		return true
	}

	closure := map[nodeID]bool{}
	queue := []nodeID{start}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if closure[current] {
			continue
		}

		closure[current] = true

		queue = append(queue, g.sources[current]...)
	}

	siteAddress := strings.Join(splitAddress(site), ".")

	for node := range closure {
		if node.module == siteModule && addressesOverlap(node.address, siteAddress) {
			return true
		}
	}

	return false
}

// addressesOverlap reports whether one canonical address contains the other.
func addressesOverlap(left, right string) bool {
	return left == right ||
		strings.HasPrefix(left, right+".") ||
		strings.HasPrefix(right, left+".")
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
	if c.graph == nil || c.unbounded {
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

// ContainsObservable reports whether the cone reaches anything an execution
// could observe or a test could kill: a resource or data node, an output, a
// check, or a contract construct (validation, precondition, postcondition —
// killable via expect_failures, per the C2 structural guard). A cone with no
// observable node supports the static Unobservable shortcut; anything else
// must execute.
func (c Cone) ContainsObservable() bool {
	if c.unbounded {
		return true
	}

	for node := range c.members {
		if isResourceBlock(node) {
			return true
		}

		if _, owned := c.graph.owners[node]; owned {
			return true
		}

		if observableAddress(node.address) {
			return true
		}
	}

	return false
}

// observableAddress reports an output, check or contract-construct address.
func observableAddress(address string) bool {
	segments := strings.Split(address, ".")
	if segments[0] == outputBlock || segments[0] == checkBlock {
		return true
	}

	for _, segment := range segments {
		switch segment {
		case "validation", "precondition", "postcondition":
			return true
		default:
		}
	}

	return false
}

// ContainsResource reports whether the cone reaches a resource: directly, or
// through the same-resource attribute union.
func (c Cone) ContainsResource(moduleRel, address string) bool {
	if c.unbounded {
		return true
	}

	node := nodeID{module: moduleRel, address: address}

	return c.contains(node)
}

// ConeOfPayloadAddress resolves a payload-form address — module path
// included — and returns its forward cone, for the supplemental comparator's
// module-wiring cases.
func (g *Graph) ConeOfPayloadAddress(address string) (Cone, bool) {
	parsed := ParseAddr(address)

	module, ok := g.moduleByPath[strings.Join(parsed.ModulePath, ".")]
	if !ok {
		return Cone{}, false
	}

	node, ok := g.resolveNode(module, parsed.Parts)
	if !ok {
		return Cone{}, false
	}

	return g.forwardCone(node), true
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
	unbounded := false

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

		if g.unbounded[current] || strings.HasPrefix(current.address, providerBlock+".") {
			unbounded = true
		}

		queue = append(queue, g.dependents[current]...)
	}

	return Cone{
		graph: g, members: members, touched: touched,
		moduleID: start.module, unbounded: unbounded,
	}
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

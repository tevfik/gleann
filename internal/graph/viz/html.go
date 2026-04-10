//go:build treesitter

// Package viz generates self-contained interactive HTML graph visualizations
// from the KuzuDB code graph. Uses vis.js (loaded from CDN) for rendering.
package viz

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"strings"

	"github.com/tevfik/gleann/internal/graph/community"
)

// Options configures the visualization output.
type Options struct {
	Title     string // page title (default: "gleann Code Graph")
	MaxNodes  int    // limit nodes rendered (default: 500)
	Physics   bool   // enable physics simulation (default: true)
	ShowFiles bool   // include CodeFile nodes (default: false)
}

// DefaultOptions returns sensible defaults.
func DefaultOptions() Options {
	return Options{
		Title:    "gleann Code Graph",
		MaxNodes: 500,
		Physics:  true,
	}
}

// visNode is a vis.js compatible node object.
type visNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Title string `json:"title"` // tooltip
	Group int    `json:"group"` // community ID
	Size  int    `json:"size"`  // based on degree
	Shape string `json:"shape"`
	Color string `json:"color,omitempty"`
}

// visEdge is a vis.js compatible edge object.
type visEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Color string `json:"color,omitempty"`
	Width int    `json:"width,omitempty"`
	Title string `json:"title,omitempty"`
}

// kindColors maps node kinds to colors.
var kindColors = map[string]string{
	"function":  "#4CAF50",
	"method":    "#2196F3",
	"type":      "#FF9800",
	"struct":    "#FF9800",
	"interface": "#9C27B0",
	"class":     "#9C27B0",
	"file":      "#607D8B",
	"const":     "#795548",
	"var":       "#795548",
	"script":    "#9E9E9E",
}

// kindShapes maps node kinds to vis.js shapes.
var kindShapes = map[string]string{
	"function":  "dot",
	"method":    "dot",
	"type":      "diamond",
	"struct":    "diamond",
	"interface": "triangle",
	"class":     "triangle",
	"file":      "square",
}

// RenderHTML writes a self-contained HTML file with the interactive graph visualization.
func RenderHTML(w io.Writer, g *community.Graph, result *community.Result, opts Options) error {
	if opts.Title == "" {
		opts.Title = "gleann Code Graph"
	}
	if opts.MaxNodes <= 0 {
		opts.MaxNodes = 500
	}

	// Build membership lookup.
	membership := make(map[string]int)
	for _, c := range result.Communities {
		for _, nid := range c.Nodes {
			membership[nid] = c.ID
		}
	}

	// God node set for highlighting.
	godSet := make(map[string]bool)
	for _, gn := range result.GodNodes {
		godSet[gn.ID] = true
	}

	// Build vis.js nodes.
	var nodes []visNode
	nodeIDs := g.NodeIDs()
	if len(nodeIDs) > opts.MaxNodes {
		nodeIDs = nodeIDs[:opts.MaxNodes]
	}

	nodeSet := make(map[string]bool, len(nodeIDs))
	for _, id := range nodeIDs {
		nodeSet[id] = true
	}

	for _, id := range nodeIDs {
		n := g.GetNode(id)
		if n == nil {
			continue
		}
		if !opts.ShowFiles && n.Kind == "file" {
			continue
		}

		label := n.Name
		if label == "" {
			label = shortID(id)
		}

		shape := kindShapes[n.Kind]
		if shape == "" {
			shape = "dot"
		}
		color := kindColors[n.Kind]
		if color == "" {
			color = "#9E9E9E"
		}

		deg := g.Degree(id)
		size := 8 + deg*2
		if size > 40 {
			size = 40
		}

		if godSet[id] {
			color = "#F44336" // red for god nodes
			size += 5
		}

		tooltip := fmt.Sprintf("<b>%s</b><br>Kind: %s<br>File: %s<br>Degree: %d<br>Community: %d",
			html.EscapeString(id), html.EscapeString(n.Kind),
			html.EscapeString(n.File), deg, membership[id])

		nodes = append(nodes, visNode{
			ID:    id,
			Label: label,
			Title: tooltip,
			Group: membership[id],
			Size:  size,
			Shape: shape,
			Color: color,
		})
	}

	// Build vis.js edges.
	var edges []visEdge
	edgeList := g.Edges()
	for _, e := range edgeList {
		if !nodeSet[e.From] || !nodeSet[e.To] {
			continue
		}
		color := "#cccccc"
		width := 1
		if membership[e.From] != membership[e.To] {
			color = "#ff5252" // red for cross-community
			width = 2
		}
		edges = append(edges, visEdge{
			From:  e.From,
			To:    e.To,
			Color: color,
			Width: width,
		})
	}

	nodesJSON, _ := json.Marshal(nodes)
	edgesJSON, _ := json.Marshal(edges)
	commJSON, _ := json.Marshal(result.Communities)
	godJSON, _ := json.Marshal(result.GodNodes)

	_, err := fmt.Fprintf(w, htmlTemplate,
		html.EscapeString(opts.Title),
		html.EscapeString(opts.Title),
		result.NodeCount, result.EdgeCount, len(result.Communities),
		result.Modularity,
		string(nodesJSON),
		string(edgesJSON),
		string(commJSON),
		string(godJSON),
		opts.Physics,
	)
	return err
}

func shortID(id string) string {
	// Take last segment after last '/'  or '.'
	if i := strings.LastIndex(id, "."); i >= 0 {
		return id[i+1:]
	}
	if i := strings.LastIndex(id, "/"); i >= 0 {
		return id[i+1:]
	}
	return id
}

const htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%s</title>
<script src="https://unpkg.com/vis-network/standalone/umd/vis-network.min.js"></script>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; background: #1a1a2e; color: #eee; }
#container { display: flex; height: 100vh; }
#graph { flex: 1; background: #16213e; }
#sidebar { width: 320px; background: #0f3460; padding: 16px; overflow-y: auto; border-left: 1px solid #333; }
.sidebar-section { margin-bottom: 16px; }
.sidebar-section h3 { font-size: 13px; color: #8888cc; text-transform: uppercase; margin-bottom: 8px; letter-spacing: 1px; }
.stat { display: flex; justify-content: space-between; padding: 4px 0; font-size: 13px; }
.stat-value { color: #64ffda; font-weight: 600; }
.node-info { background: #1a1a3e; padding: 12px; border-radius: 8px; font-size: 12px; display: none; }
.node-info h4 { color: #64ffda; margin-bottom: 8px; }
.node-info p { margin: 4px 0; }
.community-list { max-height: 200px; overflow-y: auto; }
.community-item { padding: 6px 8px; font-size: 12px; border-radius: 4px; margin: 2px 0; background: #1a1a3e; cursor: pointer; }
.community-item:hover { background: #2a2a4e; }
.god-list { max-height: 200px; overflow-y: auto; }
.god-item { padding: 6px 8px; font-size: 12px; border-radius: 4px; margin: 2px 0; background: #1a1a3e; }
.god-item .deg { color: #ff5252; font-weight: 600; }
#search { width: 100%%; padding: 8px; background: #1a1a3e; border: 1px solid #333; color: #eee; border-radius: 4px; margin-bottom: 12px; }
.legend { display: flex; flex-wrap: wrap; gap: 8px; }
.legend-item { display: flex; align-items: center; gap: 4px; font-size: 11px; }
.legend-dot { width: 10px; height: 10px; border-radius: 50%%; }
</style>
</head>
<body>
<div id="container">
  <div id="graph"></div>
  <div id="sidebar">
    <h2 style="margin-bottom:12px;font-size:16px;">%s</h2>
    <input id="search" type="text" placeholder="Search nodes...">
    <div class="sidebar-section">
      <h3>Statistics</h3>
      <div class="stat"><span>Nodes</span><span class="stat-value">%d</span></div>
      <div class="stat"><span>Edges</span><span class="stat-value">%d</span></div>
      <div class="stat"><span>Communities</span><span class="stat-value">%d</span></div>
      <div class="stat"><span>Modularity</span><span class="stat-value">%.3f</span></div>
    </div>
    <div class="sidebar-section">
      <h3>Legend</h3>
      <div class="legend">
        <div class="legend-item"><div class="legend-dot" style="background:#4CAF50"></div>function</div>
        <div class="legend-item"><div class="legend-dot" style="background:#2196F3"></div>method</div>
        <div class="legend-item"><div class="legend-dot" style="background:#FF9800"></div>type</div>
        <div class="legend-item"><div class="legend-dot" style="background:#9C27B0"></div>interface</div>
        <div class="legend-item"><div class="legend-dot" style="background:#607D8B"></div>file</div>
        <div class="legend-item"><div class="legend-dot" style="background:#F44336"></div>god node</div>
      </div>
    </div>
    <div class="sidebar-section" id="nodeInfoSection">
      <h3>Selected Node</h3>
      <div class="node-info" id="nodeInfo">
        <h4 id="niName"></h4>
        <p>Kind: <span id="niKind"></span></p>
        <p>File: <span id="niFile"></span></p>
        <p>Degree: <span id="niDeg"></span></p>
        <p>Community: <span id="niComm"></span></p>
      </div>
    </div>
    <div class="sidebar-section">
      <h3>Communities</h3>
      <div class="community-list" id="commList"></div>
    </div>
    <div class="sidebar-section">
      <h3>God Nodes</h3>
      <div class="god-list" id="godList"></div>
    </div>
  </div>
</div>
<script>
var nodesData = %s;
var edgesData = %s;
var communities = %s;
var godNodes = %s;
var physicsEnabled = %t;

var nodes = new vis.DataSet(nodesData);
var edges = new vis.DataSet(edgesData);
var container = document.getElementById('graph');
var data = { nodes: nodes, edges: edges };
var options = {
  physics: { enabled: physicsEnabled, solver: 'forceAtlas2Based', forceAtlas2Based: { gravitationalConstant: -50, centralGravity: 0.005 } },
  interaction: { hover: true, tooltipDelay: 200, multiselect: true },
  edges: { arrows: { to: { enabled: true, scaleFactor: 0.5 } }, smooth: { type: 'continuous' } },
  nodes: { font: { color: '#eee', size: 11 }, borderWidth: 1 }
};
var network = new vis.Network(container, data, options);

// Node click handler.
network.on('click', function(params) {
  if (params.nodes.length > 0) {
    var nodeId = params.nodes[0];
    var node = nodesData.find(function(n){ return n.id === nodeId; });
    if (node) {
      document.getElementById('nodeInfo').style.display = 'block';
      document.getElementById('niName').textContent = node.id;
      document.getElementById('niKind').textContent = node.shape || '';
      document.getElementById('niFile').textContent = '';
      document.getElementById('niDeg').textContent = node.size || '';
      document.getElementById('niComm').textContent = node.group || '';
    }
  } else {
    document.getElementById('nodeInfo').style.display = 'none';
  }
});

// Community list.
var commList = document.getElementById('commList');
communities.forEach(function(c) {
  var div = document.createElement('div');
  div.className = 'community-item';
  div.innerHTML = '<b>' + c.label + '</b> <span style="color:#64ffda">' + c.node_count + ' nodes</span>';
  div.onclick = function() {
    network.selectNodes(c.nodes.filter(function(nid){ return nodes.get(nid); }));
    network.fit({nodes: c.nodes.filter(function(nid){ return nodes.get(nid); }), animation: true});
  };
  commList.appendChild(div);
});

// God nodes list.
var godList = document.getElementById('godList');
godNodes.forEach(function(g) {
  var div = document.createElement('div');
  div.className = 'god-item';
  div.innerHTML = '<b>' + g.name + '</b> <span class="deg">deg=' + g.total_degree + '</span> <span style="color:#888">' + g.kind + '</span>';
  div.style.cursor = 'pointer';
  div.onclick = function() {
    if (nodes.get(g.id)) { network.selectNodes([g.id]); network.focus(g.id, {scale: 1.5, animation: true}); }
  };
  godList.appendChild(div);
});

// Search.
document.getElementById('search').addEventListener('input', function(e) {
  var q = e.target.value.toLowerCase();
  if (!q) { nodes.update(nodesData.map(function(n){ return {id: n.id, hidden: false}; })); return; }
  nodesData.forEach(function(n) {
    var match = n.id.toLowerCase().includes(q) || n.label.toLowerCase().includes(q);
    nodes.update({id: n.id, hidden: !match});
  });
});
</script>
</body>
</html>`

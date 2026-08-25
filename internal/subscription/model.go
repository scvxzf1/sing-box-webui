package subscription

import "time"

type Subscription struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	URL                   string     `json:"url"`
	AutoUpdate            bool       `json:"autoUpdate"`
	UpdateIntervalMinutes int        `json:"updateIntervalMinutes"`
	Active                bool       `json:"active"`
	SelectedNodeID        string     `json:"selectedNodeId,omitempty"`
	LastUpdated           *time.Time `json:"lastUpdated,omitempty"`
	LastError             string     `json:"lastError,omitempty"`
	LastFetchPath         string     `json:"lastFetchPath,omitempty"`
	ETag                  string     `json:"etag,omitempty"`
	LastModified          string     `json:"lastModified,omitempty"`
	ManualNodeIDs         []string   `json:"manualNodeIds,omitempty"`
	Nodes                 []Node     `json:"nodes"`
}

type ImportedRuleCondition struct {
	Type   string
	Values []string
}

type ImportedRule struct {
	Name              string
	Conditions        []ImportedRuleCondition
	Action            string
	Supported         bool
	UnsupportedReason string
	Source            string
}

type Node struct {
	ID                string    `json:"id"`
	OriginalLink      string    `json:"originalLink,omitempty"`
	Name              string    `json:"name"`
	Type              string    `json:"type"`
	Server            string    `json:"server"`
	Port              uint16    `json:"port"`
	Username          string    `json:"username,omitempty"`
	Password          string    `json:"password,omitempty"`
	UUID              string    `json:"uuid,omitempty"`
	Method            string    `json:"method,omitempty"`
	Security          string    `json:"security,omitempty"`
	AlterID           int       `json:"alterId,omitempty"`
	Flow              string    `json:"flow,omitempty"`
	TLS               TLS       `json:"tls"`
	Transport         Transport `json:"transport"`
	CongestionControl string    `json:"congestionControl,omitempty"`
	UDPRelayMode      string    `json:"udpRelayMode,omitempty"`
	Obfs              string    `json:"obfs,omitempty"`
	ObfsPassword      string    `json:"obfsPassword,omitempty"`
}

type TLS struct {
	Enabled         bool     `json:"enabled"`
	ServerName      string   `json:"serverName,omitempty"`
	Insecure        bool     `json:"insecure,omitempty"`
	ALPN            []string `json:"alpn,omitempty"`
	UTLSFingerprint string   `json:"utlsFingerprint,omitempty"`
	Reality         Reality  `json:"reality"`
}

type Reality struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"publicKey,omitempty"`
	ShortID   string `json:"shortId,omitempty"`
}

type Transport struct {
	Type        string            `json:"type,omitempty"`
	Path        string            `json:"path,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	ServiceName string            `json:"serviceName,omitempty"`
}

type NodeView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Server   string `json:"server"`
	Port     uint16 `json:"port"`
	TLS      bool   `json:"tls"`
	Selected bool   `json:"selected"`
}

type View struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	URL                   string     `json:"url"`
	AutoUpdate            bool       `json:"autoUpdate"`
	UpdateIntervalMinutes int        `json:"updateIntervalMinutes"`
	Active                bool       `json:"active"`
	SelectedNodeID        string     `json:"selectedNodeId,omitempty"`
	LastUpdated           *time.Time `json:"lastUpdated,omitempty"`
	LastError             string     `json:"lastError,omitempty"`
	LastFetchPath         string     `json:"lastFetchPath,omitempty"`
	NodeCount             int        `json:"nodeCount"`
	Nodes                 []NodeView `json:"nodes,omitempty"`
}

func toView(subscription Subscription, includeNodes bool) View {
	displayURL := redactURL(subscription.URL)
	view := View{
		ID:                    subscription.ID,
		Name:                  subscription.Name,
		URL:                   displayURL,
		AutoUpdate:            subscription.AutoUpdate,
		UpdateIntervalMinutes: subscription.UpdateIntervalMinutes,
		Active:                subscription.Active,
		SelectedNodeID:        subscription.SelectedNodeID,
		LastUpdated:           subscription.LastUpdated,
		LastError:             redactURLInText(subscription.LastError, subscription.URL),
		LastFetchPath:         subscription.LastFetchPath,
		NodeCount:             len(subscription.Nodes),
	}
	if includeNodes {
		view.Nodes = make([]NodeView, 0, len(subscription.Nodes))
		for _, node := range subscription.Nodes {
			view.Nodes = append(view.Nodes, toNodeView(node, subscription.SelectedNodeID))
		}
	}
	return view
}

func toNodeView(node Node, selectedNodeID string) NodeView {
	return NodeView{
		ID:       node.ID,
		Name:     node.Name,
		Type:     node.Type,
		Server:   node.Server,
		Port:     node.Port,
		TLS:      node.TLS.Enabled,
		Selected: node.ID == selectedNodeID,
	}
}

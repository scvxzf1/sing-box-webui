package routing

const BuiltinGlobalID = "builtin-global-proxy"

type Origin string

const (
	OriginBuiltin      Origin = "builtin"
	OriginManual       Origin = "manual"
	OriginSubscription Origin = "subscription"
)

type Action string

const (
	ActionProxy  Action = "proxy"
	ActionDirect Action = "direct"
	ActionBlock  Action = "block"
)

type Condition struct {
	Type   string   `json:"type"`
	Values []string `json:"values,omitempty"`
}

type Rule struct {
	ID                string      `json:"id"`
	Name              string      `json:"name"`
	Enabled           bool        `json:"enabled"`
	Origin            Origin      `json:"origin"`
	SubscriptionID    string      `json:"subscriptionId,omitempty"`
	SubscriptionName  string      `json:"subscriptionName,omitempty"`
	Conditions        []Condition `json:"conditions,omitempty"`
	Action            Action      `json:"action"`
	Supported         bool        `json:"supported"`
	UnsupportedReason string      `json:"unsupportedReason,omitempty"`
	Source            string      `json:"source,omitempty"`
	Position          int         `json:"position"`
	Locked            bool        `json:"locked,omitempty"`
}

type CreateInput struct {
	Name       string      `json:"name"`
	Enabled    bool        `json:"enabled"`
	Conditions []Condition `json:"conditions"`
	Action     Action      `json:"action"`
}

type UpdateInput struct {
	Name       *string      `json:"name,omitempty"`
	Enabled    *bool        `json:"enabled,omitempty"`
	Conditions *[]Condition `json:"conditions,omitempty"`
	Action     *Action      `json:"action,omitempty"`
}

type PoolRule struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Enabled    bool        `json:"enabled"`
	Conditions []Condition `json:"conditions"`
	Action     Action      `json:"action"`
	Position   int         `json:"position"`
}

type RulePool struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Enabled  bool       `json:"enabled"`
	Position int        `json:"position"`
	Rules    []PoolRule `json:"rules"`
}

type CreatePoolInput struct {
	Name    string        `json:"name"`
	Enabled bool          `json:"enabled"`
	Rules   []CreateInput `json:"rules"`
}

type UpdatePoolInput struct {
	Name    *string        `json:"name,omitempty"`
	Enabled *bool          `json:"enabled,omitempty"`
	Rules   *[]CreateInput `json:"rules,omitempty"`
}

func builtinGlobalRule() Rule {
	return Rule{
		ID: BuiltinGlobalID, Name: "全局代理", Enabled: true, Origin: OriginBuiltin,
		Action: ActionProxy, Supported: true, Locked: true, Position: 1 << 30,
	}
}

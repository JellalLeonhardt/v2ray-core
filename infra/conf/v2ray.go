package conf

import (
	"encoding/json"
	"strings"

	"v2ray.com/core"
	"v2ray.com/core/app/dispatcher"
	"v2ray.com/core/app/proxyman"
	"v2ray.com/core/app/stats"
	"v2ray.com/core/common/serial"
)

var (
	inboundConfigLoader = NewJSONConfigLoader(ConfigCreatorCache{
		"dokodemo-door": func() interface{} { return new(DokodemoConfig) },
		"http":          func() interface{} { return new(HttpServerConfig) },
		"shadowsocks":   func() interface{} { return new(ShadowsocksServerConfig) },
		"socks":         func() interface{} { return new(SocksServerConfig) },
		"vmess":         func() interface{} { return new(VMessInboundConfig) },
		"mtproto":       func() interface{} { return new(MTProtoServerConfig) },
	}, "protocol", "settings")

	outboundConfigLoader = NewJSONConfigLoader(ConfigCreatorCache{
		"blackhole":   func() interface{} { return new(BlackholeConfig) },
		"freedom":     func() interface{} { return new(FreedomConfig) },
		"http":        func() interface{} { return new(HttpClientConfig) },
		"shadowsocks": func() interface{} { return new(ShadowsocksClientConfig) },
		"vmess":       func() interface{} { return new(VMessOutboundConfig) },
		"socks":       func() interface{} { return new(SocksClientConfig) },
		"mtproto":     func() interface{} { return new(MTProtoClientConfig) },
		"dns":         func() interface{} { return new(DnsOutboundConfig) },
	}, "protocol", "settings")
)

func toProtocolList(s []string) ([]proxyman.KnownProtocols, error) {
	kp := make([]proxyman.KnownProtocols, 0, 8)
	for _, p := range s {
		switch strings.ToLower(p) {
		case "http":
			kp = append(kp, proxyman.KnownProtocols_HTTP)
		case "https", "tls", "ssl":
			kp = append(kp, proxyman.KnownProtocols_TLS)
		default:
			return nil, newError("Unknown protocol: ", p)
		}
	}
	return kp, nil
}

type SniffingConfig struct {
	Enabled      bool        `json:"enabled"`
	DestOverride *StringList `json:"destOverride"`
}

func (c *SniffingConfig) Build() (*proxyman.SniffingConfig, error) {
	var p []string
	if c.DestOverride != nil {
		for _, domainOverride := range *c.DestOverride {
			switch strings.ToLower(domainOverride) {
			case "http":
				p = append(p, "http")
			case "tls", "https", "ssl":
				p = append(p, "tls")
			default:
				return nil, newError("unknown protocol: ", domainOverride)
			}
		}
	}

	return &proxyman.SniffingConfig{
		Enabled:             c.Enabled,
		DestinationOverride: p,
	}, nil
}

type MuxConfig struct {
	Enabled     bool   `json:"enabled"`
	Concurrency uint16 `json:"concurrency"`
}

func (c *MuxConfig) GetConcurrency() uint16 {
	if c.Concurrency == 0 {
		return 8
	}
	return c.Concurrency
}

type InboundDetourAllocationConfig struct {
	Strategy    string  `json:"strategy"`
	Concurrency *uint32 `json:"concurrency"`
	RefreshMin  *uint32 `json:"refresh"`
}

// Build implements Buildable.
func (c *InboundDetourAllocationConfig) Build() (*proxyman.AllocationStrategy, error) {
	config := new(proxyman.AllocationStrategy)
	switch strings.ToLower(c.Strategy) {
	case "always":
		config.Type = proxyman.AllocationStrategy_Always
	case "random":
		config.Type = proxyman.AllocationStrategy_Random
	case "external":
		config.Type = proxyman.AllocationStrategy_External
	default:
		return nil, newError("unknown allocation strategy: ", c.Strategy)
	}
	if c.Concurrency != nil {
		config.Concurrency = &proxyman.AllocationStrategy_AllocationStrategyConcurrency{
			Value: *c.Concurrency,
		}
	}

	if c.RefreshMin != nil {
		config.Refresh = &proxyman.AllocationStrategy_AllocationStrategyRefresh{
			Value: *c.RefreshMin,
		}
	}

	return config, nil
}

type InboundDetourConfig struct {
	Protocol       string                         `json:"protocol"`
	PortRange      *PortRange                     `json:"port"`
	ListenOn       *Address                       `json:"listen"`
	Settings       *json.RawMessage               `json:"settings"`
	Tag            string                         `json:"tag"`
	Allocation     *InboundDetourAllocationConfig `json:"allocate"`
	StreamSetting  *StreamConfig                  `json:"streamSettings"`
	DomainOverride *StringList                    `json:"domainOverride"`
	SniffingConfig *SniffingConfig                `json:"sniffing"`
}

// Build implements Buildable.
func (c *InboundDetourConfig) Build() (*core.InboundHandlerConfig, error) {
	receiverSettings := &proxyman.ReceiverConfig{}

	if c.PortRange == nil {
		return nil, newError("port range not specified in InboundDetour.")
	}
	receiverSettings.PortRange = c.PortRange.Build()

	if c.ListenOn != nil {
		if c.ListenOn.Family().IsDomain() {
			return nil, newError("unable to listen on domain address: ", c.ListenOn.Domain())
		}
		receiverSettings.Listen = c.ListenOn.Build()
	}
	if c.Allocation != nil {
		concurrency := -1
		if c.Allocation.Concurrency != nil && c.Allocation.Strategy == "random" {
			concurrency = int(*c.Allocation.Concurrency)
		}
		portRange := int(c.PortRange.To - c.PortRange.From + 1)
		if concurrency >= 0 && concurrency >= portRange {
			return nil, newError("not enough ports. concurrency = ", concurrency, " ports: ", c.PortRange.From, " - ", c.PortRange.To)
		}

		as, err := c.Allocation.Build()
		if err != nil {
			return nil, err
		}
		receiverSettings.AllocationStrategy = as
	}
	if c.StreamSetting != nil {
		ss, err := c.StreamSetting.Build()
		if err != nil {
			return nil, err
		}
		receiverSettings.StreamSettings = ss
	}
	if c.SniffingConfig != nil {
		s, err := c.SniffingConfig.Build()
		if err != nil {
			return nil, newError("failed to build sniffing config").Base(err)
		}
		receiverSettings.SniffingSettings = s
	}
	if c.DomainOverride != nil {
		kp, err := toProtocolList(*c.DomainOverride)
		if err != nil {
			return nil, newError("failed to parse inbound detour config").Base(err)
		}
		receiverSettings.DomainOverride = kp
	}

	settings := []byte("{}")
	if c.Settings != nil {
		settings = ([]byte)(*c.Settings)
	}
	rawConfig, err := inboundConfigLoader.LoadWithID(settings, c.Protocol)
	if err != nil {
		return nil, newError("failed to load inbound detour config.").Base(err)
	}
	if dokodemoConfig, ok := rawConfig.(*DokodemoConfig); ok {
		receiverSettings.ReceiveOriginalDestination = dokodemoConfig.Redirect
	}
	ts, err := rawConfig.(Buildable).Build()
	if err != nil {
		return nil, err
	}

	return &core.InboundHandlerConfig{
		Tag:              c.Tag,
		ReceiverSettings: serial.ToTypedMessage(receiverSettings),
		ProxySettings:    serial.ToTypedMessage(ts),
	}, nil
}

type OutboundDetourConfig struct {
	Protocol      string           `json:"protocol"`
	SendThrough   *Address         `json:"sendThrough"`
	Tag           string           `json:"tag"`
	Settings      *json.RawMessage `json:"settings"`
	StreamSetting *StreamConfig    `json:"streamSettings"`
	ProxySettings *ProxyConfig     `json:"proxySettings"`
	MuxSettings   *MuxConfig       `json:"mux"`
	DispatchLog   bool             `json:dispatchLog`
}

// Build implements Buildable.
func (c *OutboundDetourConfig) Build() (*core.OutboundHandlerConfig, error) {
	senderSettings := &proxyman.SenderConfig{}

	if c.SendThrough != nil {
		address := c.SendThrough
		if address.Family().IsDomain() {
			return nil, newError("unable to send through: " + address.String())
		}
		senderSettings.Via = address.Build()
	}

	if c.StreamSetting != nil {
		ss, err := c.StreamSetting.Build()
		if err != nil {
			return nil, err
		}
		senderSettings.StreamSettings = ss
	}

	if c.ProxySettings != nil {
		ps, err := c.ProxySettings.Build()
		if err != nil {
			return nil, newError("invalid outbound detour proxy settings.").Base(err)
		}
		senderSettings.ProxySettings = ps
	}

	if c.MuxSettings != nil && c.MuxSettings.Enabled {
		senderSettings.MultiplexSettings = &proxyman.MultiplexingConfig{
			Enabled:     true,
			Concurrency: uint32(c.MuxSettings.GetConcurrency()),
		}
	}

	settings := []byte("{}")
	if c.Settings != nil {
		settings = ([]byte)(*c.Settings)
	}
	rawConfig, err := outboundConfigLoader.LoadWithID(settings, c.Protocol)
	if err != nil {
		return nil, newError("failed to parse to outbound detour config.").Base(err)
	}
	ts, err := rawConfig.(Buildable).Build()
	if err != nil {
		return nil, err
	}

	return &core.OutboundHandlerConfig{
		SenderSettings: serial.ToTypedMessage(senderSettings),
		Tag:            c.Tag,
		ProxySettings:  serial.ToTypedMessage(ts),
		DispatchLog:    c.DispatchLog,
	}, nil
}

type StatsConfig struct{}

func (c *StatsConfig) Build() (*stats.Config, error) {
	return &stats.Config{}, nil
}

type Config struct {
	Port            uint16                 `json:"port"` // Port of this Point server. Deprecated.
	LogConfig       *LogConfig             `json:"log"`
	RouterConfig    *RouterConfig          `json:"routing"`
	DNSConfig       *DnsConfig             `json:"dns"`
	InboundConfigs  []InboundDetourConfig  `json:"inbounds"`
	OutboundConfigs []OutboundDetourConfig `json:"outbounds"`
	InboundConfig   *InboundDetourConfig   `json:"inbound"`        // Deprecated.
	OutboundConfig  *OutboundDetourConfig  `json:"outbound"`       // Deprecated.
	InboundDetours  []InboundDetourConfig  `json:"inboundDetour"`  // Deprecated.
	OutboundDetours []OutboundDetourConfig `json:"outboundDetour"` // Deprecated.
	Transport       *TransportConfig       `json:"transport"`
	Policy          *PolicyConfig          `json:"policy"`
	Api             *ApiConfig             `json:"api"`
	Stats           *StatsConfig           `json:"stats"`
	Reverse         *ReverseConfig         `json:"reverse"`
}

func applyTransportConfig(s *StreamConfig, t *TransportConfig) {
	if s.TCPSettings == nil {
		s.TCPSettings = t.TCPConfig
	}
	if s.KCPSettings == nil {
		s.KCPSettings = t.KCPConfig
	}
	if s.WSSettings == nil {
		s.WSSettings = t.WSConfig
	}
	if s.HTTPSettings == nil {
		s.HTTPSettings = t.HTTPConfig
	}
	if s.DSSettings == nil {
		s.DSSettings = t.DSConfig
	}
}

// Build implements Buildable.
func (c *Config) Build() (*core.Config, error) {
	config := &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.InboundConfig{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
		},
	}

	if c.Api != nil {
		apiConf, err := c.Api.Build()
		if err != nil {
			return nil, err
		}
		config.App = append(config.App, serial.ToTypedMessage(apiConf))
	}

	if c.Stats != nil {
		statsConf, err := c.Stats.Build()
		if err != nil {
			return nil, err
		}
		config.App = append(config.App, serial.ToTypedMessage(statsConf))
	}

	if c.LogConfig != nil {
		config.App = append(config.App, serial.ToTypedMessage(c.LogConfig.Build()))
	} else {
		config.App = append(config.App, serial.ToTypedMessage(DefaultLogConfig()))
	}

	if c.RouterConfig != nil {
		routerConfig, err := c.RouterConfig.Build()
		if err != nil {
			return nil, err
		}
		config.App = append(config.App, serial.ToTypedMessage(routerConfig))
	}

	if c.DNSConfig != nil {
		dnsApp, err := c.DNSConfig.Build()
		if err != nil {
			return nil, newError("failed to parse DNS config").Base(err)
		}
		config.App = append(config.App, serial.ToTypedMessage(dnsApp))
	}

	if c.Policy != nil {
		pc, err := c.Policy.Build()
		if err != nil {
			return nil, err
		}
		config.App = append(config.App, serial.ToTypedMessage(pc))
	}

	if c.Reverse != nil {
		r, err := c.Reverse.Build()
		if err != nil {
			return nil, err
		}
		config.App = append(config.App, serial.ToTypedMessage(r))
	}

	var inbounds []InboundDetourConfig

	if c.InboundConfig != nil {
		inbounds = append(inbounds, *c.InboundConfig)
	}

	if len(c.InboundDetours) > 0 {
		inbounds = append(inbounds, c.InboundDetours...)
	}

	if len(c.InboundConfigs) > 0 {
		inbounds = append(inbounds, c.InboundConfigs...)
	}

	// Backward compatibility.
	if len(inbounds) > 0 && inbounds[0].PortRange == nil && c.Port > 0 {
		inbounds[0].PortRange = &PortRange{
			From: uint32(c.Port),
			To:   uint32(c.Port),
		}
	}

	for _, rawInboundConfig := range inbounds {
		if c.Transport != nil {
			if rawInboundConfig.StreamSetting == nil {
				rawInboundConfig.StreamSetting = &StreamConfig{}
			}
			applyTransportConfig(rawInboundConfig.StreamSetting, c.Transport)
		}
		ic, err := rawInboundConfig.Build()
		if err != nil {
			return nil, err
		}
		config.Inbound = append(config.Inbound, ic)
	}

	var outbounds []OutboundDetourConfig

	if c.OutboundConfig != nil {
		outbounds = append(outbounds, *c.OutboundConfig)
	}

	if len(c.OutboundDetours) > 0 {
		outbounds = append(outbounds, c.OutboundDetours...)
	}

	if len(c.OutboundConfigs) > 0 {
		outbounds = append(outbounds, c.OutboundConfigs...)
	}

	for _, rawOutboundConfig := range outbounds {
		if c.Transport != nil {
			if rawOutboundConfig.StreamSetting == nil {
				rawOutboundConfig.StreamSetting = &StreamConfig{}
			}
			applyTransportConfig(rawOutboundConfig.StreamSetting, c.Transport)
		}
		oc, err := rawOutboundConfig.Build()
		if err != nil {
			return nil, err
		}
		config.Outbound = append(config.Outbound, oc)
	}

	return config, nil
}

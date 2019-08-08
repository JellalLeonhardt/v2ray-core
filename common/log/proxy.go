package log

import (
	"strings"
)

type ProxyMessage struct {
	To          string
	OutboundTag string
}

func (m *ProxyMessage) String() string {
	builder := strings.Builder{}
	builder.WriteString("Proxy From")
	builder.WriteByte(' ')
	builder.WriteString(m.OutboundTag)
	builder.WriteByte(' ')
	builder.WriteString("TO")
	builder.WriteByte(' ')
	builder.WriteString(m.To)
	return builder.String()
}

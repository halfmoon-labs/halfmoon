package maixcam

import (
	"github.com/halfmoon-labs/halfmoon/pkg/bus"
	"github.com/halfmoon-labs/halfmoon/pkg/channels"
	"github.com/halfmoon-labs/halfmoon/pkg/config"
)

func init() {
	channels.RegisterFactory("maixcam", func(cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
		return NewMaixCamChannel(cfg.Channels.MaixCam, b)
	})
}

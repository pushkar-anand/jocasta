package topomap

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pushkar-anand/jocasta/internal/topology"
)

func TestRateReadsAsPeopleSayIt(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "1 Gbps", Rate(1_000_000_000))
	assert.Equal(t, "2.5 Gbps", Rate(2_500_000_000))
	assert.Equal(t, "10 Gbps", Rate(10_000_000_000))
	assert.Equal(t, "100 Mbps", Rate(100_000_000))
	assert.Equal(t, "867 Mbps", Rate(866_600_000))
	assert.Equal(t, "29 Mbps", Rate(28_900_000))
	assert.Equal(t, "6.5 Mbps", Rate(6_500_000))
	assert.Empty(t, Rate(0))
}

func TestSpeedLabelSaysHowALinkRuns(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "1 Gbps", SpeedLabel(topology.Speed{Rate: 1_000_000_000, FullDuplex: true}))
	assert.Equal(t, "100 Mbps · half duplex", SpeedLabel(topology.Speed{Rate: 100_000_000}))
	assert.Empty(t, SpeedLabel(topology.Speed{}))
}

func TestRadioLabelSaysHowAWiFiClientRuns(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "867 Mbps down · 650 Mbps up · -54 dBm",
		RadioLabel(topology.Radio{Down: 866_600_000, Up: 650_000_000, Signal: -54}))
	assert.Equal(t, "-70 dBm", RadioLabel(topology.Radio{Signal: -70}))
	assert.Empty(t, RadioLabel(topology.Radio{}))
}

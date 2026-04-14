package podbridge5

import "fmt"

type volumeSetupAction int

const (
	volumeSetupActionSkip volumeSetupAction = iota
	volumeSetupActionCreate
	volumeSetupActionReuse
	volumeSetupActionOverwrite
)

func planVolumeSetup(mode VolumeMode, exists bool) (volumeSetupAction, error) {
	switch mode {
	case ModeOverwrite:
		return volumeSetupActionOverwrite, nil
	case ModeSkip:
		if exists {
			return volumeSetupActionSkip, nil
		}
		return volumeSetupActionCreate, nil
	case ModeUpdate:
		if exists {
			return volumeSetupActionReuse, nil
		}
		return volumeSetupActionCreate, nil
	default:
		return 0, fmt.Errorf("unknown volume mode: %d", mode)
	}
}

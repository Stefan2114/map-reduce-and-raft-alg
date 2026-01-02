package config

import "go-map-reduce-framework/mr"

var DefaultTier1Config = mr.CoordinatorConfig{
	Tier:                 mr.Tier1NoFT,
	SpeculativeThreshold: 0.0,
	CheckpointFile:       "",
	BackupCoordinator:    false,
	GossipEnabled:        false,
}

var DefaultTier2Config = mr.CoordinatorConfig{
	Tier:                 mr.Tier2Speculative,
	SpeculativeThreshold: 0.8,
	CheckpointFile:       "",
	BackupCoordinator:    false,
	GossipEnabled:        false,
}

var DefaultTier3Config = mr.CoordinatorConfig{
	Tier:                 mr.Tier3Replicated,
	SpeculativeThreshold: 0.8,
	CheckpointFile:       "mr-state.json",
	BackupCoordinator:    true,
	GossipEnabled:        false,
}

var DefaultTier4Config = mr.CoordinatorConfig{
	Tier:                 mr.Tier4Gossip,
	SpeculativeThreshold: 0.8,
	CheckpointFile:       "mr-state.json",
	BackupCoordinator:    true,
	GossipEnabled:        true,
}

func GetConfigForTier(tier int) mr.CoordinatorConfig {
	switch tier {
	case 1:
		return DefaultTier1Config
	case 2:
		return DefaultTier2Config
	case 3:
		return DefaultTier3Config
	case 4:
		return DefaultTier4Config
	default:
		return DefaultTier1Config
	}
}

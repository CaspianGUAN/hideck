package config

import yaml "go.yaml.in/yaml/v3"

func UpdateSystemInFile(path string, system SystemConfig) error {
	return updateConfigInFile(path, func(root *yaml.Node) error {
		systemNode := getMapValue(root, "system")
		if systemNode == nil || systemNode.Kind != yaml.MappingNode {
			systemNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setMapNode(root, "system", systemNode)
		}
		setMapBool(systemNode, "openwrt_dynamic_interfaces", system.OpenWRTDynamicInterfaces)
		return nil
	})
}

// UpdateRecordingRetentionInFile writes server.recording_retention_days.
func UpdateRecordingRetentionInFile(path string, days int) error {
	return updateConfigInFile(path, func(root *yaml.Node) error {
		serverNode := getMapValue(root, "server")
		if serverNode == nil || serverNode.Kind != yaml.MappingNode {
			serverNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setMapNode(root, "server", serverNode)
		}
		setMapInt(serverNode, "recording_retention_days", days)
		return nil
	})
}

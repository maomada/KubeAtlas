package main

import "github.com/maomada/KubeAtlas/internal/contextname"

func clusterShortName(contextName string) string {
	return contextname.ShortName(contextName)
}

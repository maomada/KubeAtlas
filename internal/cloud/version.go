package cloud

// Version is the KubeAtlas binary version. Set at build time with:
//
//	-ldflags "-X github.com/maomada/KubeAtlas/internal/cloud.Version=v1.5.2"
//
// Falls back to "dev" for local builds.
var Version = "dev"

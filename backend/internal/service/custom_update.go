package service

import infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

// Custom builds are upgraded by replacing the image, never the running binary.
var ErrCustomBuildUpdateDisabled = infraerrors.Forbidden(
	"CUSTOM_BUILD_UPDATE_DISABLED",
	"Custom builds must be upgraded or rolled back using your custom Docker image.",
)

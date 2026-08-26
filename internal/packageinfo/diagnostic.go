package packageinfo

type DiagnosticCode string

const (
	CodeManifestMissing       DiagnosticCode = "PACKAGE_MANIFEST_MISSING"
	CodeManifestCorrupt       DiagnosticCode = "PACKAGE_MANIFEST_CORRUPT"
	CodePlatformUnsupported   DiagnosticCode = "PACKAGE_PLATFORM_UNSUPPORTED"
	CodeModeUnexpected        DiagnosticCode = "PACKAGE_MODE_UNEXPECTED"
	CodePathEscape            DiagnosticCode = "PACKAGE_PATH_ESCAPE"
	CodeComponentDuplicate    DiagnosticCode = "PACKAGE_COMPONENT_DUPLICATE"
	CodeComponentMissing      DiagnosticCode = "PACKAGE_COMPONENT_MISSING"
	CodeComponentCorrupt      DiagnosticCode = "PACKAGE_COMPONENT_CORRUPT"
	CodeBuildMismatch         DiagnosticCode = "PACKAGE_BUILD_MISMATCH"
	CodeEmbeddedAssetMismatch DiagnosticCode = "PACKAGE_EMBEDDED_ASSET_MISMATCH"
	CodeFileUnexpected        DiagnosticCode = "PACKAGE_FILE_UNEXPECTED"
	CodeContentForbidden      DiagnosticCode = "PACKAGE_CONTENT_FORBIDDEN"
	CodePinSetCorrupt         DiagnosticCode = "PACKAGE_PIN_SET_CORRUPT"
	CodeLocalRAGVersionDrift  DiagnosticCode = "PACKAGE_LOCAL_RAG_VERSION_DRIFT"
	CodeOpenAPIDrift          DiagnosticCode = "PACKAGE_OPENAPI_DRIFT"
	CodeConsumerFixtureDrift  DiagnosticCode = "PACKAGE_CONSUMER_FIXTURE_DRIFT"
)

// DiagnosticError contains only stable codes and closed component kinds. It
// deliberately omits package roots, usernames, raw JSON, and file contents.
type DiagnosticError struct {
	Code      DiagnosticCode
	Component ComponentKind
}

func (e DiagnosticError) Error() string {
	if e.Component.Valid() {
		return string(e.Code) + ": " + string(e.Component)
	}
	return string(e.Code)
}

func diagnostic(code DiagnosticCode, component ComponentKind) error {
	return DiagnosticError{Code: code, Component: component}
}

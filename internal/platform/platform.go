// Package platform adapts Ryotunes' XDG-based layout to the host OS.
//
// Ryotunes resolves every directory through the XDG Base Directory variables.
// On Windows those are normally unset, so this package points them at the
// conventional Windows locations before any other package reads them:
//
//	XDG_CONFIG_HOME  %APPDATA%\Ryotunes\config
//	XDG_DATA_HOME    %LOCALAPPDATA%\Ryotunes\data
//	XDG_STATE_HOME   %LOCALAPPDATA%\Ryotunes\state
//	XDG_CACHE_HOME   %LOCALAPPDATA%\Ryotunes\cache
//	XDG_RUNTIME_DIR  %TEMP%\Ryotunes
//
// Values the user already set are left alone. On other systems the package
// does nothing. Import it (blank) from low-level packages so its init runs
// first.
package platform

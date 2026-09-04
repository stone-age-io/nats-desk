package scheme

// macOS resolves URL schemes from CFBundleURLTypes in an application bundle's
// Info.plist. A bare executable has no bundle and therefore cannot claim one,
// and building a bundle would mean shipping a directory rather than a file -
// which is the packaging the whole project is arranged to avoid.
//
// Nothing is lost that matters. The offline page's button simply does nothing
// on macOS, and the page says so; the binary is still the way in.
func register() error { return nil }

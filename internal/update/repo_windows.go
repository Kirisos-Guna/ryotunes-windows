//go:build windows

package update

// repositorySlug is the Windows port's release repository. Windows builds
// cannot install packages, so update checks only link to its release page.
const repositorySlug = "Kirisos-Guna/ryotunes-windows"

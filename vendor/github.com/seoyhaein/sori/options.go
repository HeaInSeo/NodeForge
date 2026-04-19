package sori

// PackageOptions controls the preferred core packaging path.
//
// This option surface is part of the stable core candidate contract.
type PackageOptions struct {
	ConfigBlob        []byte
	RequireConfigBlob bool
}

// PushOptions controls the preferred core push path.
//
// This option surface is part of the stable core candidate contract.
type PushOptions struct {
	Target RemoteTarget
}

// FetchOptions controls the preferred core fetch path.
//
// This option surface is part of the stable core candidate contract.
type FetchOptions struct {
	Concurrency             int
	RequireEmptyDestination bool
}

// ReferrerOptions controls the experimental referrer helpers.
//
// Experimental: this option surface belongs to the referrer API and is not yet
// part of the frozen core contract.
type ReferrerOptions struct {
	Target RemoteTarget
}

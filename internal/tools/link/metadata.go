package link

import (
	"context"

	"github.com/vishvananda/netlink"

	"github.com/networkservicemesh/sdk/pkg/networkservice/utils/metadata"
)

type key struct{}

// Store sets the netlink.Link stored in per Connection.Id metadata.
func Store(ctx context.Context, isClient bool, link netlink.Link) {
	metadata.Map(ctx, isClient).Store(key{}, link)
}

// Delete deletes the netlink.Link stored in per Connection.Id metadata
func Delete(ctx context.Context, isClient bool) {
	metadata.Map(ctx, isClient).Delete(key{})
}

// Load returns the netlink.Link stored in per Connection.Id metadata, or nil if no
// value is present.
// The ok result indicates whether value was found in the per Connection.Id metadata.
func Load(ctx context.Context, isClient bool) (value netlink.Link, ok bool) {
	rawValue, ok := metadata.Map(ctx, isClient).Load(key{})
	if !ok {
		return
	}
	value, ok = rawValue.(netlink.Link)
	return value, ok
}

// LoadOrStore returns the existing netlink.Link stored in per Connection.Id metadata if present.
// Otherwise, it stores and returns the given nterface_types.InterfaceIndex.
// The loaded result is true if the value was loaded, false if stored.
func LoadOrStore(ctx context.Context, isClient bool, link netlink.Link) (value netlink.Link, ok bool) {
	rawValue, ok := metadata.Map(ctx, isClient).LoadOrStore(key{}, link)
	if !ok {
		return
	}
	value, ok = rawValue.(netlink.Link)
	return value, ok
}

// LoadAndDelete deletes the netlink.Link stored in per Connection.Id metadata,
// returning the previous value if any. The loaded result reports whether the key was present.
func LoadAndDelete(ctx context.Context, isClient bool) (value netlink.Link, ok bool) {
	rawValue, ok := metadata.Map(ctx, isClient).LoadAndDelete(key{})
	if !ok {
		return
	}
	value, ok = rawValue.(netlink.Link)
	return value, ok
}

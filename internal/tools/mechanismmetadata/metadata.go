package mechanismmetadata

import (
	"context"

	"github.com/networkservicemesh/api/pkg/api/networkservice"

	"github.com/networkservicemesh/sdk/pkg/networkservice/utils/metadata"
)

type metadataKey struct{}

func Store(ctx context.Context, isClient bool, mech *networkservice.Mechanism) {
	metadata.Map(ctx, isClient).Store(metadataKey{}, mech)
}

func Load(ctx context.Context, isClient bool) (*networkservice.Mechanism, bool) {
	v, ok := metadata.Map(ctx, isClient).Load(metadataKey{})
	if !ok {
		return nil, false
	}
	return v.(*networkservice.Mechanism), true
}

func LoadAndDelete(ctx context.Context, isClient bool) *networkservice.Mechanism {
	v, ok := metadata.Map(ctx, isClient).LoadAndDelete(metadataKey{})
	if !ok {
		return nil
	}
	return v.(*networkservice.Mechanism)
}

func Delete(ctx context.Context, isClient bool) {
	metadata.Map(ctx, isClient).Delete(metadataKey{})
}

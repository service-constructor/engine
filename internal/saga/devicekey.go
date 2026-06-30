package saga

import "context"

// StaticDeviceKeyResolver returns a single device public key regardless of user
// or kid. It exists for local runs and tests where there is no real device
// registry. In production, implement DeviceKeyResolver against the wallet's
// device-registration service.
type StaticDeviceKeyResolver struct {
	PEM string
}

func (r StaticDeviceKeyResolver) DevicePublicKeyPEM(_ context.Context, _ string, _ string) (string, error) {
	return r.PEM, nil
}

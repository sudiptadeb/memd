package feature

import "github.com/sudiptadeb/memd/server/internal/doctrine"

// RegisterDoctrines seeds the Live doctrine store with each feature's base
// doctrine, so a super admin can edit feature doctrines at runtime alongside the
// global one. The id for a feature's doctrine is doctrine.FeatureID(key).
func RegisterDoctrines(live *doctrine.Live, reg *Registry) {
	for _, f := range reg.List() {
		live.Register(doctrine.FeatureID(f.Key), f.Name+" doctrine", f.BaseDoctrine())
	}
}

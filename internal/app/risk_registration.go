package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	riskgate "github.com/zouyi/eco-guardian/internal/risk/gate"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

var ErrRiskRegistration = errors.New("risk integration registration failed")

type RiskVersionSink interface {
	RegisterRiskVersionContributor(versioningrevision.VersionContributor) error
}

type RiskWorkerLifecycle interface {
	Start(context.Context) error
	Close(context.Context) error
}

type RiskIntegration struct {
	Contributor riskgate.VersionContributor
	Provider    riskgate.Provider
	Worker      RiskWorkerLifecycle
}

// RiskImplementationVersion is derived only from source-controlled contract
// semantics. Optional impact-provider availability and process identity are
// deliberately excluded.
func RiskImplementationVersion() string {
	sum := sha256.Sum256([]byte("eco-guardian/risk/v1\x00comparison-v1\x00cohort-v1\x00structural-v1\x00report-v1\x00metric-resource-v1:[0.8,1.2]"))
	return hex.EncodeToString(sum[:])
}

func RegisterRiskIntegration(versions RiskVersionSink, gates *versioninggate.Registry, worker RiskWorkerLifecycle) (RiskIntegration, error) {
	if versions == nil || gates == nil || worker == nil {
		return RiskIntegration{}, ErrRiskRegistration
	}
	if _, err := riskcontract.V1Registries(); err != nil {
		return RiskIntegration{}, errors.Join(ErrRiskRegistration, err)
	}
	implementation := RiskImplementationVersion()
	integration := RiskIntegration{Contributor: riskgate.VersionContributor{Implementation: implementation}, Provider: riskgate.Provider{Implementation: implementation}, Worker: worker}
	if err := versions.RegisterRiskVersionContributor(integration.Contributor); err != nil {
		return RiskIntegration{}, errors.Join(ErrRiskRegistration, err)
	}
	if err := gates.RegisterProvider(integration.Provider); err != nil {
		return RiskIntegration{}, errors.Join(ErrRiskRegistration, err)
	}
	return integration, nil
}

func (integration RiskIntegration) Start(ctx context.Context) error {
	if integration.Worker == nil {
		return ErrRiskRegistration
	}
	return integration.Worker.Start(ctx)
}

func (integration RiskIntegration) Close(ctx context.Context) error {
	if integration.Worker == nil {
		return nil
	}
	return integration.Worker.Close(ctx)
}

func RegisterUnavailableRisk(versions RiskVersionSink) error {
	if versions == nil {
		return ErrRiskRegistration
	}
	return versions.RegisterRiskVersionContributor(riskgate.VersionContributor{})
}

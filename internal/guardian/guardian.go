// Package guardian provides interfaces and types for managing Host Guardian
// Service (HGS) guardians in TrustedLaunch / vTPM enabled clusters.
package guardian

import "context"

// Guardian represents a cluster-shared HGS guardian that manages attestation
// policies and key protectors for shielded VMs.
type Guardian struct {
	// Name is the friendly name of the guardian.
	Name string
	// CertificateThumbprint is the thumbprint of the guardian's signing certificate.
	CertificateThumbprint string
	// AttestationURL is the HGS attestation endpoint.
	AttestationURL string
	// KeyProtectionURL is the HGS key protection endpoint.
	KeyProtectionURL string
}

// HostRegistration contains the information needed to register a cluster node
// with the Host Guardian Service.
type HostRegistration struct {
	// HostName is the FQDN of the cluster node.
	HostName string
	// GuardianName is the name of the guardian to register against.
	GuardianName string
	// AttestationMode specifies the attestation type (e.g., "TPM", "HostKey").
	AttestationMode string
}

// Manager defines operations for managing HGS guardians and host registration.
type Manager interface {
	// CreateGuardian provisions a new cluster-shared guardian with the given
	// name and certificate thumbprint. Returns the created guardian metadata.
	CreateGuardian(ctx context.Context, name string, certThumbprint string) (*Guardian, error)

	// GetGuardian retrieves guardian metadata by name.
	GetGuardian(ctx context.Context, name string) (*Guardian, error)

	// DeleteGuardian removes a guardian by name. Returns an error if the
	// guardian has active host registrations.
	DeleteGuardian(ctx context.Context, name string) error

	// RegisterHost registers a cluster node with the Host Guardian Service
	// so that it can run shielded / TrustedLaunch VMs.
	RegisterHost(ctx context.Context, reg HostRegistration) error

	// UnregisterHost removes a host registration from the HGS.
	UnregisterHost(ctx context.Context, hostName string) error

	// ListHosts returns all hosts registered with the specified guardian.
	ListHosts(ctx context.Context, guardianName string) ([]string, error)
}

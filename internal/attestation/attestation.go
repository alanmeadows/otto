// Package attestation provides interfaces and types for vTPM-based
// attestation in TrustedLaunch enabled clusters.
package attestation

import "context"

// PlatformState represents the measured boot state of a virtual machine.
type PlatformState struct {
	// VMID is the unique identifier for the virtual machine.
	VMID string
	// PCRValues maps PCR indices to their measured SHA-256 digests.
	PCRValues map[int]string
	// SecureBootEnabled indicates whether UEFI Secure Boot was active.
	SecureBootEnabled bool
	// VTPMPresent indicates whether a vTPM was present during boot.
	VTPMPresent bool
}

// HealthCertificate is issued by the HGS when a VM passes attestation.
type HealthCertificate struct {
	// VMID is the identifier of the attested VM.
	VMID string
	// Thumbprint is the certificate thumbprint.
	Thumbprint string
	// IsValid indicates whether the certificate is currently valid.
	IsValid bool
}

// AttestationResult holds the outcome of an attestation request.
type AttestationResult struct {
	// Attested is true when the platform state passed validation.
	Attested bool
	// Certificate is the health certificate, non-nil only when Attested is true.
	Certificate *HealthCertificate
	// Reason describes why attestation failed (empty on success).
	Reason string
}

// Verifier defines operations for verifying vTPM-based platform attestation.
type Verifier interface {
	// Attest validates the platform state of a VM against the guardian's
	// attestation policy and returns the result.
	Attest(ctx context.Context, guardianName string, state PlatformState) (*AttestationResult, error)

	// GetCertificate retrieves the current health certificate for a VM.
	GetCertificate(ctx context.Context, vmID string) (*HealthCertificate, error)

	// RevokeCertificate invalidates a previously issued health certificate.
	RevokeCertificate(ctx context.Context, vmID string) error
}

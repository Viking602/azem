package securityscan

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var identifierSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func NewID(prefix string) (string, error) {
	if !identifierSegment.MatchString(prefix) {
		return "", fmt.Errorf("security scan: invalid ID prefix %q", prefix)
	}
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("security scan: generate ID: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(value[:]), nil
}

func TargetIdentity(remote, canonicalRepository string) (targetID, sanitizedRemote string, err error) {
	material := ""
	if strings.TrimSpace(remote) != "" {
		sanitizedRemote, err = SanitizeRemote(remote)
		if err != nil {
			return "", "", err
		}
		material = "git:" + sanitizedRemote
	} else {
		canonicalRepository, err = filepath.Abs(canonicalRepository)
		if err != nil {
			return "", "", fmt.Errorf("security scan: canonical repository: %w", err)
		}
		material = "directory:" + filepath.Clean(canonicalRepository)
	}
	digest := sha256.Sum256([]byte(material))
	return "tgt_" + hex.EncodeToString(digest[:])[:24], sanitizedRemote, nil
}

func SanitizeRemote(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("security scan: remote must be an absolute URL")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "ssh" {
		return "", fmt.Errorf("security scan: unsupported remote scheme %q", parsed.Scheme)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("security scan: remote must not contain credentials, query, or fragment")
	}
	parsed.Path = strings.TrimSuffix(path.Clean("/"+strings.TrimPrefix(parsed.Path, "/")), "/")
	return parsed.String(), nil
}

func FindingIdentityFor(scanID, targetID string, finding Finding) (FindingFingerprints, string, string, error) {
	if strings.TrimSpace(scanID) == "" || strings.TrimSpace(targetID) == "" {
		return FindingFingerprints{}, "", "", fmt.Errorf("security scan: scan and target IDs are required")
	}
	if err := finding.ValidateDraft(); err != nil {
		return FindingFingerprints{}, "", "", err
	}
	material := strings.Join([]string{
		IdentityAlgorithm,
		targetID,
		finding.RuleID,
		finding.Identity.Anchor,
		finding.Identity.Instance,
	}, "\x00")
	primaryDigest := sha256.Sum256([]byte(material))
	primary := IdentityAlgorithm + ":sha256:" + hex.EncodeToString(primaryDigest[:])
	findingDigest := sha256.Sum256([]byte(primary))
	occurrenceDigest := sha256.Sum256([]byte(scanID + "\x00" + primary))
	return FindingFingerprints{Algorithm: IdentityAlgorithm, Primary: primary},
		"csf_" + hex.EncodeToString(findingDigest[:])[:24],
		"occ_" + hex.EncodeToString(occurrenceDigest[:])[:24], nil
}

func BindFindingIdentities(scanID, targetID string, findings []Finding) ([]Finding, error) {
	bound := make([]Finding, len(findings))
	seen := make(map[string]struct{}, len(findings))
	for index, finding := range findings {
		fingerprints, findingID, occurrenceID, err := FindingIdentityFor(scanID, targetID, finding)
		if err != nil {
			return nil, fmt.Errorf("security scan: finding %d identity: %w", index, err)
		}
		if _, exists := seen[findingID]; exists {
			return nil, fmt.Errorf("security scan: duplicate finding identity %s", findingID)
		}
		seen[findingID] = struct{}{}
		finding.Fingerprints = fingerprints
		finding.FindingID = findingID
		finding.OccurrenceID = occurrenceID
		if finding.Extensions == nil {
			finding.Extensions = map[string]any{}
		}
		finding.Extensions["identityAlgorithm"] = IdentityAlgorithm
		bound[index] = finding
	}
	return bound, nil
}

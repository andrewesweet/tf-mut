package engine_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	tfconfig "github.com/andrewesweet/tf-mut/internal/config"
	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
)

// The shipped `security-aws` pack (M5c.2). The pack is embedded in the binary
// under its reserved name, resolved by `--pack security-aws` without any
// registration, and carries exactly the entries the M5-0.3 census and
// admission measurement enabled (docs/research/18-m5-pack-seed-census.md,
// "Decision" and "Per-entry result"). The pin below is that published
// admitted list; the tests hold the shipped file against it.

// shippedSecurityAWSPin is one row of the published admitted list: every
// field the contract requires the shipped pack to state.
type shippedSecurityAWSPin struct {
	id           string
	sourceRule   string
	sourceField  string
	resourceType string
	attribute    string
	form         string
	from         bool
}

const shippedSecurityAWSName = "security-aws"

const (
	shippedLicenceApache = "Apache-2.0"
	shippedLicenceMIT    = "MIT"

	shippedResourceS3PublicAccessBlock = "aws_s3_bucket_public_access_block"
)

// checkovPin and trivyPin build one pin row in the census's published shape:
// every admitted entry is a flip from true to false, and the licence follows
// the catalogue.
func checkovPin(id, rule, resource, attribute string) shippedSecurityAWSPin {
	return shippedSecurityAWSPin{
		id: id, sourceRule: rule, sourceField: shippedLicenceApache,
		resourceType: resource, attribute: attribute, form: mutation.FormFlip, from: true,
	}
}

func trivyPin(id, rule, resource, attribute string) shippedSecurityAWSPin {
	return shippedSecurityAWSPin{
		id: id, sourceRule: rule, sourceField: shippedLicenceMIT,
		resourceType: resource, attribute: attribute, form: mutation.FormFlip, from: true,
	}
}

// shippedSecurityAWSPinEntries is the admitted ten, in the census's own
// order. The witnessed set is not empty, and every entry states its provenance.
//
//nolint:gochecknoglobals // an immutable pin.
var shippedSecurityAWSPinEntries = []shippedSecurityAWSPin{
	checkovPin("checkov-aws-7", "CKV_AWS_7", "aws_kms_key", "enable_key_rotation"),
	checkovPin("checkov-aws-53", "CKV_AWS_53", shippedResourceS3PublicAccessBlock, "block_public_acls"),
	checkovPin("checkov-aws-54", "CKV_AWS_54", shippedResourceS3PublicAccessBlock, "block_public_policy"),
	checkovPin("checkov-aws-55", "CKV_AWS_55", shippedResourceS3PublicAccessBlock, "ignore_public_acls"),
	checkovPin("checkov-aws-56", "CKV_AWS_56", shippedResourceS3PublicAccessBlock, "restrict_public_buckets"),
	trivyPin("trivy-aws-0065", "AWS-0065", "aws_kms_key", "enable_key_rotation"),
	trivyPin("trivy-aws-0086", "AWS-0086", shippedResourceS3PublicAccessBlock, "block_public_acls"),
	trivyPin("trivy-aws-0087", "AWS-0087", shippedResourceS3PublicAccessBlock, "block_public_policy"),
	trivyPin("trivy-aws-0091", "AWS-0091", shippedResourceS3PublicAccessBlock, "ignore_public_acls"),
	trivyPin("trivy-aws-0093", "AWS-0093", shippedResourceS3PublicAccessBlock, "restrict_public_buckets"),
}

// admittedSecurityAWSEntries is the published admitted list the admission
// measurement (measure-security-aws) is held against; the shipped pack must
// be exactly it. Kept beside the shipped-pack pin so one list governs both.
//
//nolint:gochecknoglobals // an immutable admission result.
var admittedSecurityAWSEntries = []string{
	"checkov-aws-7",
	"checkov-aws-53",
	"checkov-aws-54",
	"checkov-aws-55",
	"checkov-aws-56",
	"trivy-aws-0065",
	"trivy-aws-0086",
	"trivy-aws-0087",
	"trivy-aws-0091",
	"trivy-aws-0093",
}

// TestTheShippedSecurityAWSPackShipsExactlyTheAdmittedEntries is the golden
// test for the embedded pack: every entry's label, provenance, site and form
// equal the census's published admitted list, in its order, every entry
// states its source rule and licence, and the reserved names are exactly the
// shipped packs'.
func TestTheShippedSecurityAWSPackShipsExactlyTheAdmittedEntries(t *testing.T) {
	t.Parallel()

	pack, found := mutation.ShippedPack(shippedSecurityAWSName)
	if !found {
		t.Fatal("no shipped pack is embedded under the reserved name security-aws")
	}

	ids := make([]string, 0, len(pack.Entries))
	for _, entry := range pack.Entries {
		ids = append(ids, entry.ID)
	}

	want := slices.Sorted(slices.Values(admittedSecurityAWSEntries))
	if !slices.Equal(slices.Sorted(slices.Values(ids)), want) {
		t.Fatalf("the shipped pack carries %v, want the published admitted list %v", ids, want)
	}

	if len(pack.Entries) != len(shippedSecurityAWSPinEntries) {
		t.Fatalf("the shipped pack carries %d entries, want %d",
			len(pack.Entries), len(shippedSecurityAWSPinEntries))
	}

	for position, pin := range shippedSecurityAWSPinEntries {
		entry := pack.Entries[position]

		got := shippedSecurityAWSPin{
			id: entry.ID, sourceRule: entry.SourceRule, sourceField: entry.SourceLicence,
			resourceType: entry.ResourceType, attribute: entry.Attribute, form: entry.Form,
			from: literalBool(t, entry.From),
		}

		if !reflect.DeepEqual(got, pin) {
			t.Errorf("entry %d = %+v, want the published pin %+v", position, got, pin)
		}

		if literalText(t, entry.To) != "false" {
			t.Errorf("entry %s: to = %s, want false", entry.ID, literalText(t, entry.To))
		}
	}

	if reserved := mutation.ReservedPackNames(); !reflect.DeepEqual(reserved, []string{shippedSecurityAWSName}) {
		t.Fatalf("reserved names = %v, want exactly the shipped pack's", reserved)
	}
}

// literalBool renders a literal the pin can compare.
func literalBool(t *testing.T, value cty.Value) bool {
	t.Helper()

	if value.Type() != cty.Bool {
		t.Fatalf("literal is a %s, want a bool", value.Type().FriendlyName())
	}

	return value.True()
}

// literalText renders a literal for a failure message.
func literalText(t *testing.T, value cty.Value) string {
	t.Helper()

	switch value.Type() {
	case cty.Bool:
		return strconv.FormatBool(value.True())
	case cty.String:
		return value.AsString()
	default:
		t.Fatalf("literal is a %s, want a bool or string", value.Type().FriendlyName())

		return ""
	}
}

// TestTheShippedSecurityAWSPackResolvesWithoutRegistration: `--pack
// security-aws` selects the embedded pack with no pack block anywhere. On the
// offline fixture no AWS schema is loaded, so the preview succeeds with every
// entry reported as matched-no-site — the pack's entries reached the
// generator, and no mutant carries origins where no site can match.
func TestTheShippedSecurityAWSPackResolvesWithoutRegistration(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, packsFixture)

	result := packPreview(t, module, shippedSecurityAWSName)

	for _, pin := range shippedSecurityAWSPinEntries {
		reported := slices.ContainsFunc(result.Warnings, func(warning string) bool {
			return strings.Contains(warning, `pack "`+shippedSecurityAWSName+`": entry "`+pin.id+`"`)
		})

		if !reported {
			t.Errorf("preview's pack summary does not report shipped entry %q: %v",
				pin.id, result.Warnings)
		}
	}

	for _, mutant := range result.Mutants {
		if len(mutant.Origins) > 0 {
			t.Fatalf("the shipped pack generated %s at %s on a module no entry can match",
				mutant.Operator, mutant.Site)
		}
	}
}

// TestAUserPackCannotShadowTheShippedSecurityAWSPack: a `pack "security-aws"`
// registration is refused by name while `--pack security-aws` still resolves
// to the embedded pack — the refusal does not take the shipped pack down.
func TestAUserPackCannotShadowTheShippedSecurityAWSPack(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, packsFixture)
	writeFile(t, filepath.Join(module, "packs", "shadow.hcl"),
		"entry \"usurper\" {\n  resource_type = \"terraform_data\"\n  attribute = \"input\"\n"+
			"  form = \"flip\"\n  from = true\n  to = false\n}\n")
	writeFile(t, filepath.Join(module, packConfigFile),
		"pack \""+shippedSecurityAWSName+"\" {\n  file = \"packs/shadow.hcl\"\n}\n")

	request := previewRequest(t, module)
	request.Packs = []string{shippedSecurityAWSName}

	if _, err := engine.Run(t.Context(), request); !errors.Is(err, tfconfig.ErrConfig) ||
		!strings.Contains(err.Error(), "shadows the reserved name") {
		t.Fatalf("the user registration was not refused: %v", err)
	}

	unregistered := copyFixture(t, packsFixture)
	shipped := packPreview(t, unregistered, shippedSecurityAWSName)

	resolved := slices.ContainsFunc(shipped.Warnings, func(warning string) bool {
		return strings.Contains(warning, `pack "`+shippedSecurityAWSName+`"`)
	})
	if !resolved {
		t.Fatalf("after refusing the shadowing registration, --pack security-aws did not "+
			"resolve to the shipped pack: %v", shipped.Warnings)
	}
}

// TestTheShippedPackSelectionLeavesTheDefaultPopulationAlone: a run with no
// `--pack` carries neither a pack tier nor origins, and on this binary the
// shipped pack's presence changes nothing about it. Selecting the pack on a
// module its entries cannot match adds no mutant either: the population count
// is unchanged.
func TestTheShippedPackSelectionLeavesTheDefaultPopulationAlone(t *testing.T) {
	t.Parallel()

	without := packPreview(t, copyFixture(t, packsFixture))

	for _, mutant := range without.Mutants {
		if len(mutant.Origins) > 0 || mutant.Tier == string(mutation.TierPack) {
			t.Fatalf("the default population carries %s at %s with origins %+v",
				mutant.Operator, mutant.Site, mutant.Origins)
		}
	}

	selected := packPreview(t, copyFixture(t, packsFixture), shippedSecurityAWSName)

	if len(selected.Mutants) != len(without.Mutants) {
		t.Fatalf("selecting the shipped pack changed the population: %d mutants, was %d",
			len(selected.Mutants), len(without.Mutants))
	}
}

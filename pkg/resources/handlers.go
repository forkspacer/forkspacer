package resources

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
	"github.com/forkspacer/forkspacer/pkg/constants/version"
)

func HandleResource(
	resourceData []byte,
	configInputs *map[string]any,
	handleHelmModule func(helmModule HelmModule) error,
	handleCustomModule func(customModule CustomModule) error,
) error {
	baseResource, err := ReadResource[BaseResource](resourceData)
	if err != nil {
		return fmt.Errorf("failed to read module type: %v", err)
	}

	versionConstraint, err := semver.NewConstraint(baseResource.ObjectMeta.SupportedOperatorVersion)
	if err != nil {
		return fmt.Errorf(
			"failed to parse supported operator version constraint %q: %w",
			baseResource.ObjectMeta.SupportedOperatorVersion, err,
		)
	}

	if ok := versionConstraint.Check(version.VersionParsed); !ok {
		return fmt.Errorf(
			"operator version %s does not satisfy supported version constraint %q",
			version.VersionParsed.String(),
			baseResource.ObjectMeta.SupportedOperatorVersion,
		)
	}

	if configInputs != nil {
		if err = baseResource.ValidateConfig(*configInputs); err != nil {
			return fmt.Errorf("failed to validate configuration: %w", err)
		}
	}

	switch baseResource.GetKind() {
	case KindHelmType:
		if handleHelmModule == nil {
			return nil
		}

		helmModule, err := ReadResource[HelmModule](resourceData)
		if err != nil {
			return fmt.Errorf("failed to read Helm manager details: %v", err)
		}

		return handleHelmModule(*helmModule)
	case KindCustomType:
		if handleCustomModule == nil {
			return nil
		}

		customModule, err := ReadResource[CustomModule](resourceData)
		if err != nil {
			return fmt.Errorf("failed to read base bundle information: %v", err)
		}

		return handleCustomModule(*customModule)
	default:
		return fmt.Errorf("unknown kind: %s", baseResource.GetKind())
	}
}

func HandleConfigItemSpec(
	configItemValue ConfigItem,
	optionSpecFunc func(val string) error,
	stringSpecFunc func(val string) error,
	integerSpecFunc func(val int) error,
	floatSpecFunc func(val float64) error,
	booleanSpecFunc func(val bool) error,
	multipleOptionsSpecFunc func(val []string) error,
) error {
	switch configItemValue.Type {
	case "option":
		if optionSpecFunc == nil {
			return nil
		}
		return optionSpecFunc(configItemValue.Spec.(string))
	case "string":
		if stringSpecFunc == nil {
			return nil
		}
		return stringSpecFunc(configItemValue.Spec.(string))
	case "integer":
		if integerSpecFunc == nil {
			return nil
		}
		return integerSpecFunc(configItemValue.Spec.(int))
	case "float":
		if floatSpecFunc == nil {
			return nil
		}
		return floatSpecFunc(configItemValue.Spec.(float64))
	case "boolean":
		if booleanSpecFunc == nil {
			return nil
		}
		return booleanSpecFunc(configItemValue.Spec.(bool))
	case "multiple-options":
		if multipleOptionsSpecFunc == nil {
			return nil
		}
		return multipleOptionsSpecFunc(configItemValue.Spec.([]string))
	default:
		return fmt.Errorf("unknown spec type: %s", configItemValue.Type)
	}
}

package resources

import (
	"fmt"
	"regexp"
	"slices"

	"go.yaml.in/yaml/v3"
)

// Validator is an interface for validating user input.
type Validator interface {
	Validate(input any) error
	IsRequired() bool
	DefaultValue() any
	IsEditable() bool
}

type ConfigItem struct {
	Type  string `yaml:"type" json:"type"`
	Name  string `yaml:"name" json:"name"`
	Alias string `yaml:"alias" json:"alias"`
	Spec  any    `yaml:"spec" json:"spec"`
}

// OptionSpec defines the spec for an "option" type.
type OptionSpec struct {
	Required bool     `yaml:"required" json:"required"`
	Default  string   `yaml:"default" json:"default"`
	Values   []string `yaml:"values" json:"values"`
	Editable bool     `yaml:"editable" json:"editable"`
}

// OptionValidator implements the Validator interface for OptionSpec.
type OptionValidator struct {
	spec OptionSpec
}

func (ov *OptionValidator) Validate(input any) error {
	val, ok := input.(string)
	if !ok {
		return fmt.Errorf("input is not a string")
	}

	if slices.Contains(ov.spec.Values, val) {
		return nil
	}
	return fmt.Errorf("invalid value '%s', must be one of %v", val, ov.spec.Values)
}

func (ov *OptionValidator) IsRequired() bool {
	return ov.spec.Required
}

func (ov *OptionValidator) DefaultValue() any {
	return ov.spec.Default
}

func (ov *OptionValidator) IsEditable() bool {
	return ov.spec.Editable
}

// StringSpec defines the spec for a "string" type.
type StringSpec struct {
	Required bool   `yaml:"required" json:"required"`
	Default  string `yaml:"default" json:"default"`
	Regex    string `yaml:"regex" json:"regex"`
	Editable bool   `yaml:"editable" json:"editable"`
}

// StringValidator implements the Validator interface for StringSpec.
type StringValidator struct {
	spec StringSpec
	re   *regexp.Regexp
}

func (sv *StringValidator) Validate(input any) error {
	val, ok := input.(string)
	if !ok {
		return fmt.Errorf("input is not a string")
	}

	if sv.re != nil && !sv.re.MatchString(val) {
		return fmt.Errorf("value '%s' does not match the required regex pattern", val)
	}

	return nil
}

func (sv *StringValidator) IsRequired() bool {
	return sv.spec.Required
}

func (sv *StringValidator) DefaultValue() any {
	return sv.spec.Default
}

func (sv *StringValidator) IsEditable() bool {
	return sv.spec.Editable
}

// IntegerSpec defines the spec for an "integer" type.
type IntegerSpec struct {
	Required bool `yaml:"required" json:"required"`
	Default  int  `yaml:"default" json:"default"`
	Min      *int `yaml:"min" json:"min"`
	Max      *int `yaml:"max" json:"max"`
	Editable bool `yaml:"editable" json:"editable"`
}

// IntegerValidator implements the Validator interface for IntegerSpec.
type IntegerValidator struct {
	spec IntegerSpec
}

func (iv *IntegerValidator) Validate(input any) error {
	var val int
	switch v := input.(type) {
	case int:
		val = v
	case float64:
		val = int(v)
	default:
		return fmt.Errorf("input is not an integer")
	}

	if iv.spec.Min != nil && val < *iv.spec.Min {
		return fmt.Errorf("value %d is less than minimum %d", val, *iv.spec.Min)
	}

	if iv.spec.Max != nil && val > *iv.spec.Max {
		return fmt.Errorf("value %d is greater than maximum %d", val, *iv.spec.Max)
	}

	return nil
}

func (iv *IntegerValidator) IsRequired() bool {
	return iv.spec.Required
}

func (iv *IntegerValidator) DefaultValue() any {
	return iv.spec.Default
}

func (iv *IntegerValidator) IsEditable() bool {
	return iv.spec.Editable
}

// FloatSpec defines the spec for a "float" type.
type FloatSpec struct {
	Required bool     `yaml:"required" json:"required"`
	Default  float64  `yaml:"default" json:"default"`
	Min      *float64 `yaml:"min" json:"min"`
	Max      *float64 `yaml:"max" json:"max"`
	Editable bool     `yaml:"editable" json:"editable"`
}

// FloatValidator implements the Validator interface for FloatSpec.
type FloatValidator struct {
	spec FloatSpec
}

func (fv *FloatValidator) Validate(input any) error {
	var val float64
	switch v := input.(type) {
	case float64:
		val = v
	case int:
		val = float64(v)
	default:
		return fmt.Errorf("input is not a float")
	}

	if fv.spec.Min != nil && val < *fv.spec.Min {
		return fmt.Errorf("value %f is less than minimum %f", val, *fv.spec.Min)
	}

	if fv.spec.Max != nil && val > *fv.spec.Max {
		return fmt.Errorf("value %f is greater than maximum %f", val, *fv.spec.Max)
	}

	return nil
}

func (fv *FloatValidator) IsRequired() bool {
	return fv.spec.Required
}

func (fv *FloatValidator) DefaultValue() any {
	return fv.spec.Default
}

func (fv *FloatValidator) IsEditable() bool {
	return fv.spec.Editable
}

// BooleanSpec defines the spec for a "boolean" type.
type BooleanSpec struct {
	Required bool `yaml:"required" json:"required"`
	Default  bool `yaml:"default" json:"default"`
	Editable bool `yaml:"editable" json:"editable"`
}

// BooleanValidator implements the Validator interface for BooleanSpec.
type BooleanValidator struct {
	spec BooleanSpec
}

func (bv *BooleanValidator) Validate(input any) error {
	_, ok := input.(bool)
	if !ok {
		return fmt.Errorf("input is not a boolean")
	}
	return nil
}

func (bv *BooleanValidator) IsRequired() bool {
	return bv.spec.Required
}

func (bv *BooleanValidator) DefaultValue() any {
	return bv.spec.Default
}

func (bv *BooleanValidator) IsEditable() bool {
	return bv.spec.Editable
}

// MultipleOptionsSpec defines the spec for a "multiple-options" type.
type MultipleOptionsSpec struct {
	Required bool     `yaml:"required" json:"required"`
	Default  []string `yaml:"default" json:"default"`
	Values   []string `yaml:"values" json:"values"`
	Min      *int     `yaml:"min" json:"min"`
	Max      *int     `yaml:"max" json:"max"`
	Editable bool     `yaml:"editable" json:"editable"`
}

// MultipleOptionsValidator implements the Validator interface for MultipleOptionsSpec.
type MultipleOptionsValidator struct {
	spec MultipleOptionsSpec
}

func (mov *MultipleOptionsValidator) Validate(input any) error {
	val, ok := input.([]any)
	if !ok {
		return fmt.Errorf("input is not an array")
	}

	if mov.spec.Min != nil && len(val) < *mov.spec.Min {
		return fmt.Errorf("must select at least %d options", *mov.spec.Min)
	}

	if mov.spec.Max != nil && len(val) > *mov.spec.Max {
		return fmt.Errorf("must select at most %d options", *mov.spec.Max)
	}

	for _, item := range val {
		strItem, ok := item.(string)
		if !ok {
			return fmt.Errorf("all options must be strings")
		}
		if !slices.Contains(mov.spec.Values, strItem) {
			return fmt.Errorf("invalid option '%s', must be one of %v", strItem, mov.spec.Values)
		}
	}

	return nil
}

func (mov *MultipleOptionsValidator) IsRequired() bool {
	return mov.spec.Required
}

func (mov *MultipleOptionsValidator) DefaultValue() any {
	return mov.spec.Default
}

func (mov *MultipleOptionsValidator) IsEditable() bool {
	return mov.spec.Editable
}

// =============================================================================
// VALIDATOR FACTORY
// =============================================================================

// ValidatorFactory is a function that creates a new Validator from a spec.
type ValidatorFactory func(spec any) (Validator, error)

var validatorFactories = make(map[string]ValidatorFactory)

// RegisterValidatorFactory registers a new validator factory for a given type.
func RegisterValidatorFactory(fieldType string, factory ValidatorFactory) {
	validatorFactories[fieldType] = factory
}

func init() {
	RegisterValidatorFactory("option", func(spec any) (Validator, error) {
		var optionSpec OptionSpec
		yamlBytes, err := yaml.Marshal(spec)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(yamlBytes, &optionSpec); err != nil {
			return nil, err
		}
		return &OptionValidator{spec: optionSpec}, nil
	})

	RegisterValidatorFactory("string", func(spec any) (Validator, error) {
		var stringSpec StringSpec
		yamlBytes, err := yaml.Marshal(spec)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(yamlBytes, &stringSpec); err != nil {
			return nil, err
		}

		var re *regexp.Regexp
		if stringSpec.Regex != "" {
			re, err = regexp.Compile(stringSpec.Regex)
			if err != nil {
				return nil, fmt.Errorf("invalid regex pattern: %w", err)
			}
		}
		return &StringValidator{spec: stringSpec, re: re}, nil
	})

	RegisterValidatorFactory("integer", func(spec any) (Validator, error) {
		var integerSpec IntegerSpec
		yamlBytes, err := yaml.Marshal(spec)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(yamlBytes, &integerSpec); err != nil {
			return nil, err
		}
		return &IntegerValidator{spec: integerSpec}, nil
	})

	RegisterValidatorFactory("float", func(spec any) (Validator, error) {
		var floatSpec FloatSpec
		yamlBytes, err := yaml.Marshal(spec)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(yamlBytes, &floatSpec); err != nil {
			return nil, err
		}
		return &FloatValidator{spec: floatSpec}, nil
	})

	RegisterValidatorFactory("boolean", func(spec any) (Validator, error) {
		var booleanSpec BooleanSpec
		yamlBytes, err := yaml.Marshal(spec)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(yamlBytes, &booleanSpec); err != nil {
			return nil, err
		}
		return &BooleanValidator{spec: booleanSpec}, nil
	})

	RegisterValidatorFactory("multiple-options", func(spec any) (Validator, error) {
		var multipleOptionsSpec MultipleOptionsSpec
		yamlBytes, err := yaml.Marshal(spec)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(yamlBytes, &multipleOptionsSpec); err != nil {
			return nil, err
		}
		return &MultipleOptionsValidator{spec: multipleOptionsSpec}, nil
	})
}

// =============================================================================
// PARSER
// =============================================================================

type InputValidator struct {
	validators map[string]Validator
}

func (inputValidator *InputValidator) ValidateUserInput(input map[string]any) error {
	// First, check for any fields in the input that are not defined in the validators.
	for inputAlias := range input {
		if _, exists := inputValidator.validators[inputAlias]; !exists {
			return fmt.Errorf("unknown field '%s' provided in input", inputAlias)
		}
	}

	// Then, proceed with the existing validation logic for defined fields.
	for alias, validator := range inputValidator.validators {
		value, ok := input[alias]
		if !ok {
			// Check if the field is required
			if validator.IsRequired() {
				return fmt.Errorf("field '%s' is required but missing", alias)
			}

			// If not required, check for a default value
			defaultValue := validator.DefaultValue()
			if defaultValue != nil {
				// Apply the default value to the input map
				// This mutates the input, which is a common pattern for applying defaults.
				input[alias] = defaultValue
			}
			continue
		}
		// If the field is present in the input, check if it's editable before validating.
		if !validator.IsEditable() {
			return fmt.Errorf("field '%s' is not editable", alias)
		}

		if err := validator.Validate(value); err != nil {
			return fmt.Errorf("validation failed for field '%s': %w", alias, err)
		}
	}
	return nil
}

func (inputValidator *InputValidator) GetValidators() map[string]Validator {
	return inputValidator.validators
}

func GenerateConfigInputValidator(configItems []ConfigItem) (*InputValidator, error) {
	validators := make(map[string]Validator)
	for _, item := range configItems {
		factory, ok := validatorFactories[item.Type]
		if !ok {
			return nil, fmt.Errorf("unregistered field type: %s", item.Type)
		}

		validator, err := factory(item.Spec)
		if err != nil {
			return nil, err
		}
		validators[item.Alias] = validator
	}

	return &InputValidator{validators: validators}, nil
}

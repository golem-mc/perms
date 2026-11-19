package core

import _ "embed"

//go:embed resources/config.yml
var defaultConfigYML []byte

//go:embed resources/luckperms_en.properties
var defaultTranslationsEn []byte

package flasher

func Catalog() ([]HardwareProfile, error) {
	s, e := BuiltinRegistry()
	if e != nil {
		return nil, e
	}
	return s.Profiles(EngineVersion, "", nil), nil
}

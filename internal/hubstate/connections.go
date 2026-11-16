package hubstate

import "github.com/maomada/KubeAtlas/internal/cloud"

func (s *Store) Connections() ConnectionSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Connections == nil {
		return ConnectionSettings{}
	}
	return *s.data.Connections
}

func (s *Store) UpdateConnections(input ConnectionSettings) (ConnectionSettings, error) {
	for _, target := range []*string{&input.LANURL, &input.PublicURL} {
		if *target == "" {
			continue
		}
		normalized, err := cloud.NormalizeHubOrigin(*target)
		if err != nil {
			return ConnectionSettings{}, failure("invalid_parameters")
		}
		*target = normalized
	}
	if ambiguousConnectionSchemes(&input) {
		return ConnectionSettings{}, failure("invalid_parameters")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.next(s.now())
	next.Connections = &input
	if err := s.commit(next); err != nil {
		return ConnectionSettings{}, err
	}
	return input, nil
}

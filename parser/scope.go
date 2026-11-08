package parser

import (
	"errors"
	"slices"

	wireless "github.com/gatework/go-wireless"
)

// CollectionScope owns the ordered queries and evidence dimensions for one
// profile. A nil request selects defaults; an empty request is invalid. LLDP is
// opt-in and needs access-point evidence, even on an unsupported platform.
type CollectionScope struct {
	dimensions, unsupported []wireless.Dimension
	queries                 []Query
	apQuery                 APQuery
}

func (s CollectionScope) Dimensions() []wireless.Dimension  { return slices.Clone(s.dimensions) }
func (s CollectionScope) Unsupported() []wireless.Dimension { return slices.Clone(s.unsupported) }
func (s CollectionScope) APNeighbors() bool                 { return s.apQuery.Valid() }
func (s CollectionScope) APQuery() APQuery                  { return s.apQuery }
func (s CollectionScope) Queries() []Query {
	queries := slices.Clone(s.queries)
	for i := range queries {
		queries[i].Dimensions = slices.Clone(queries[i].Dimensions)
	}
	return queries
}

func (p *Profile) CollectionScope(requested []wireless.Dimension) (CollectionScope, error) {
	if !p.Valid() {
		return CollectionScope{}, errors.New("wireless parser: profile required")
	}
	dimensions := slices.Clone(requested)
	if requested == nil {
		dimensions = p.Dimensions()
		dimensions = slices.DeleteFunc(dimensions, func(d wireless.Dimension) bool { return d == wireless.LLDP })
	}
	if err := ValidateNames(dimensions, false); err != nil {
		return CollectionScope{}, err
	}
	for _, dimension := range dimensions {
		if !dimension.Valid() {
			return CollectionScope{}, errors.New("wireless: invalid collection scope")
		}
	}
	appendDependency := func(required wireless.Dimension, requested ...wireless.Dimension) {
		if slices.Contains(dimensions, required) {
			return
		}
		for _, name := range requested {
			if slices.Contains(dimensions, name) {
				dimensions = append(dimensions, required)
				return
			}
		}
	}
	appendDependency(wireless.AccessPoints, wireless.LLDP)
	appendDependency(wireless.Configuration, wireless.APGroups, wireless.ServiceTemplates)
	scope := CollectionScope{dimensions: dimensions, unsupported: []wireless.Dimension{}}
	if slices.Contains(dimensions, wireless.LLDP) {
		scope.apQuery = p.APQuery()
	}
	queryDimensions := []wireless.Dimension{}
	for _, dimension := range dimensions {
		if _, err := p.Command(dimension); err != nil || dimension == wireless.LLDP && !scope.APNeighbors() {
			scope.unsupported = append(scope.unsupported, dimension)
		} else if dimension != wireless.LLDP {
			queryDimensions = append(queryDimensions, dimension)
		}
	}
	var err error
	scope.queries, err = p.Queries(queryDimensions)
	return scope, err
}

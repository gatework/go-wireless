package wireless

import "errors"

type Config struct{ profile *Profile }

func NewConfig(platform, model string) (*Config, error) {
	if platform == "" {
		platform = "h3c"
	}
	p, e := NewProfile(platform, model)
	if e != nil {
		return nil, e
	}
	return &Config{p}, nil
}
func (c *Config) Parse(content string, complete bool) (Observation, error) {
	if _, ok := c.profile.Commands()[Configuration]; !ok {
		return Observation{}, errors.New("unsupported parser dimension")
	}
	return c.profile.Parse(Configuration, content, complete)
}
func (c *Config) String() string   { return "<wireless.Config>" }
func (c *Config) GoString() string { return c.String() }

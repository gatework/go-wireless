# frozen_string_literal: true
# Run explicitly with the frozen Ruby JSON 3.1.0 oracle; never called at runtime.
# Usage: bundle exec ruby /path/to/go-wireless/scripts/export-ruby-fpconv.rb > corpus.json
require 'json'
raise 'Expected JSON 3.1.0' unless JSON::VERSION == '3.1.0'
# Offline reference evidence only. Production runtime never executes Ruby.
rng = Random.new(20261026)
bits = [0, 0x8000000000000000, 1, 0x000fffffffffffff, 0x0010000000000000, 0x7fefffffffffffff]
# Both sides of every binary exponent boundary plus deterministic random patterns.
(1...2047).each do |exp|
  boundary = exp << 52
  bits.concat([boundary - 1, boundary, boundary + 1])
end
10000.times { bits << rng.rand(1 << 64) }
cases = bits.uniq.filter_map do |word|
  value = [word].pack('Q>').unpack1('G')
  next unless value.finite?
  { bits: '%016x' % word, canonical: JSON.generate(value) }
end
puts JSON.generate({ ruby: RUBY_VERSION, json: JSON::VERSION, seed: 20261026, cases: cases })

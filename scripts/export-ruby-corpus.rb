# frozen_string_literal: true
# Read-only oracle: executes the reference specs, never modifies their workspace.
require 'json'
require 'digest'
require 'fileutils'
require 'base64'
require 'zlib'
REFERENCE = ENV.fetch('WIRELESS_RUBY_SOURCE', '/Users/wenwu.yan/Works/ruby/wireless')
DESTINATION = File.expand_path('..', __dir__)
if ENV['WIRELESS_EXPORT_CHILD']
  require File.join(REFERENCE, 'spec/spec_helper')
  module WirelessOracle
    CASES = {}
    def parse(text, complete: false)
      result = super
      if self.class.name&.match?(/\AWireless::(?:H3c|Huawei)::Parsers::/) && text.is_a?(String)
        output = result.respond_to?(:by_dimension) ? result.by_dimension.transform_values(&:to_h) : result.to_h
        entry = { parser: self.class.name, input_base64: Base64.strict_encode64(text.b), complete: complete, expected: output }
        if respond_to?(:cursor) && cursor
          entry[:source] = { checksum: cursor.checksum, states: cursor.states, unparsed_base64: Base64.strict_encode64(cursor.unparsed_content.b),
            captures: cursor.captures.map { |c| { first_line: c.line_range.begin, last_line: c.line_range.end, content_base64: Base64.strict_encode64(c.content.b), path: c.path, data: c.data, complete: c.complete? } } }
        end
        key = Digest::SHA256.hexdigest(JSON.generate(entry))
        provenance = if defined?(RSpec) && RSpec.current_example
          example = RSpec.current_example
          { file: example.metadata[:file_path], line: example.metadata[:line_number], description: example.full_description }
        else
          { file: 'scripts/export-ruby-corpus.rb', description: 'supplemental public contract probe; original fixture or synthetic supported grammar' }
        end
        (CASES[key] ||= entry.merge(id: key[0,16], provenance: []))[:provenance] << provenance unless CASES.dig(key, :provenance)&.include?(provenance)
      end
      result
    end
  end
  CtxParser::Parser.prepend(WirelessOracle)
  module WirelessFieldOracle
    CASES = {}
  end
  field_methods = %i[value mac_address ip_address integer vlan_id number duration_seconds rates_mbps channel channel_width_mhz frequency_band online_status]
  wrapper = Module.new
  field_methods.each do |name|
    wrapper.define_method(name) do |*args, **kwargs|
      entry = { method: name, args: args, kwargs: kwargs }
      begin
        output = super(*args, **kwargs)
        entry[:expected] = output
        output
      rescue ArgumentError, IPAddr::Error => error
        entry[:error] = error.class.name
        raise
      ensure
        begin
          entry[:provenance] = RSpec.current_example && { file: RSpec.current_example.metadata[:file_path], line: RSpec.current_example.metadata[:line_number] }
          key = Digest::SHA256.hexdigest(JSON.generate(entry.reject { |k, _| k == :provenance }))
          WirelessFieldOracle::CASES[key] ||= entry.merge(id: key[0,16])
        rescue JSON::GeneratorError
          # Encoding-corrupt text is covered by byte-preserving parser cases.
        end
      end
    end
  end
  Wireless::Fields.singleton_class.prepend(wrapper)
  RSpec.configure do |config|
    config.after(:suite) do
      # Original specs do not call the MAB implementation directly. Exercise its shared
      # contract with original DOT1X fixture, plus both explicit empty and truncated cases.
      Wireless::H3c::Parsers::DisplayCurrentConfiguration.parse(File.read(File.join(DESTINATION,'testdata/synthetic-complete-configuration.cfg')), complete: true)
      Wireless::H3c::Parsers::DisplayCurrentConfiguration.parse("wlan ap AP model TEST\n priority #{'9'*80}\nreturn\n", complete: true)
      Wireless::H3c::Parsers::DisplayWlanClientVerbose.parse("Total clients: 1\nMAC address: 0200-0000-0001\nAP ID: #{'9'*80}\nOnline time: #{'9'*80} days\n", complete: true)
      Wireless::H3c::Parsers::DisplayWlanClientVerbose.parse("Total clients: #{'9'*80}\n", complete: true)
      Wireless::H3c::Parsers::DisplayCurrentConfiguration.parse("wlan service-template T\n ssid \u00a0Corporate\u00a0\nreturn\n", complete: true)

      # Scoped review regressions: finite Float#round beyond int64 and Ruby IP identities.
      %w[18.5 0.5 100000000000000000000 123456789012345680000].each do |signal|
        Wireless::H3c::Parsers::DisplayWlanClientVerbose.parse("Total clients: 1\nMAC address: 0200-0000-0001\nRSSI: #{signal}\n", complete: true)
      end
      %w[::192.0.2.1 ::c000:201 ::1:0 ::ffff:ffff ::2 ::ffff ::ffff:c000:201 2001:0db8::0001].each do |address|
        Wireless::Fields.ip_address(address)
        Wireless::H3c::Parsers::DisplayWlanClientVerbose.parse("Total clients: 1\nMAC address: 0200-0000-0001\nIPv6 address: #{address}\n", complete: true)
      end
      fixture = File.read(File.join(REFERENCE, 'spec/fixtures/netstack/wireless_dot1x.txt'))
      Wireless::H3c::Parsers::DisplayMacAuthenticationConnection.parse(fixture, complete: true)
      Wireless::H3c::Parsers::DisplayMacAuthenticationConnection.parse("Total connections: 0\n", complete: true)
      Wireless::H3c::Parsers::DisplayMacAuthenticationConnection.parse(fixture, complete: false)
    end
  end
  at_exit do
    Zlib::GzipWriter.open(File.join(DESTINATION, 'testdata/ruby-parser-corpus.json.gz')) do |gzip|
      gzip.mtime = 0
      gzip.write(JSON.pretty_generate(WirelessOracle::CASES.values.sort_by { |e| [e[:parser], e[:id]] }) + "\n")
    end
    File.write(File.join(DESTINATION, 'testdata/ruby-field-corpus.json'), JSON.pretty_generate(WirelessFieldOracle::CASES.values.sort_by { |e| [e[:method], e[:id]] }) + "\n")
  end
else
  ruby = '/opt/homebrew/opt/ruby/bin/ruby'
  bundle = '/opt/homebrew/opt/ruby/bin/bundle'
  system({ 'WIRELESS_EXPORT_CHILD' => '1', 'PATH' => "/opt/homebrew/opt/ruby/bin:#{ENV['PATH']}" }, ruby, bundle, 'exec', 'rspec', '--require', File.expand_path(__FILE__), '--seed', '12345', chdir: REFERENCE) || abort('Reference specs failed')
  files = Dir.chdir(REFERENCE) { ['Gemfile', 'Gemfile.lock', *Dir['*.gemspec'], *Dir['lib/**/*.rb'], *Dir['spec/**/*'].select { |f| File.file?(f) }].sort }
  dependency = '/opt/homebrew/lib/ruby/gems/4.0.0/gems/ctx_parser-0.2.4'
  manifest = { reference: REFERENCE, head: IO.popen(['git', '-C', REFERENCE, 'rev-parse', 'HEAD'], &:read).strip,
    ruby: IO.popen([ruby, '-v'], &:read).strip, reference_status: IO.popen(['git','-C',REFERENCE,'status','--porcelain'], &:read).strip, files: files.to_h { |f| [f, Digest::SHA256.file(File.join(REFERENCE, f)).hexdigest] },
    ctx_parser: Dir.glob(File.join(dependency, 'lib/**/*.rb')).sort.to_h { |f| [f.delete_prefix(dependency + '/'), Digest::SHA256.file(f).hexdigest] } }
  manifest[:corpus] = %w[testdata/ruby-parser-corpus.json.gz testdata/ruby-field-corpus.json testdata/synthetic-complete-configuration.cfg].to_h { |f| [f,Digest::SHA256.file(File.join(DESTINATION,f)).hexdigest] }
  File.write(File.join(DESTINATION, 'docs/ruby-baseline.json'), JSON.pretty_generate(manifest) + "\n")
end

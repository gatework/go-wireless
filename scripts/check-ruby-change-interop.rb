# frozen_string_literal: true
# Optional read-only reference gate. Invoked from Go tests under bundle exec in
# the independently frozen Ruby checkout. All output lives in caller temp dirs.
require 'wireless'
require 'wireless/cli'
require 'wireless/cli/review'
require 'wireless/cli/apply'
require 'json'
require 'digest'
require 'pty'
require 'io/console'
require 'stringio'
batch, plan_path, source_path, output = ARGV
plan = JSON.parse(File.read(plan_path))
raise 'Wrong JSON oracle' unless JSON::VERSION == '3.1.0'
config = JSON.parse(JSON.generate(Wireless::Config.new(platform: :h3c_wireless).parse(File.read(source_path), complete: true).records.first))
raise 'Go fingerprint differs' unless Wireless::ChangePlan.fingerprint(config) == plan.fetch('expected_config_sha256')
raise 'Go commands differ' unless Wireless::ChangePlan.render(plan.fetch('change')) == [plan.fetch('commands'), plan.fetch('rollback')]
review_output = StringIO.new
code = Wireless::CLI::Review.new(out: review_output, error: review_output).run(['--batch', batch, '--scripts', '--output', File.join(output, 'ruby-reviews')])
raise "Ruby review failed: #{code}" unless code.zero?
review_plan = JSON.parse(File.read(Dir[File.join(output, 'ruby-reviews', '*', 'recommendations', '*.plan.json')].fetch(0)))
raise 'Ruby review fingerprint differs' unless review_plan.fetch('expected_config_sha256') == plan.fetch('expected_config_sha256')
raise 'Ruby review commands differ' unless review_plan.fetch('commands') == plan.fetch('commands')
source = File.read(source_path)
after_source = source.gsub(" cipher-suite tkip\n", '').gsub(" security-ie wpa\n", '')
after = JSON.parse(JSON.generate(Wireless::Config.new(platform: :h3c_wireless).parse(after_source, complete: true).records.first))
writes = []
reads = 0
session = Object.new
session.define_singleton_method(:read_configuration) { reads += 1; reads == 1 ? config : after }
session.define_singleton_method(:write) { |command| writes << command; true }
connections = 0
factory = lambda do |_device, settings, &run|
  raise 'Host trust widened' unless settings.fetch(:host_key_policy) == 'strict'
  connections += 1
  run.call(session)
end
master, slave = PTY.open
slave.raw!
transcript = +''
drain = Thread.new do
  loop { transcript << master.readpartial(4096) }
rescue IOError, Errno::EIO
  nil
end
token = "APPLY #{plan.fetch('device').fetch('host')} #{Digest::SHA256.file(plan_path).hexdigest[0,12]}"
master.write("#{token}\n")
code = Wireless::CLI.run(['apply', '--plan', plan_path, '--directory', File.join(output, 'ruby-executions')],
  env: {'NC_DEVICE_USERNAME' => 'offline-fixture'}, input: slave, out: slave, error: slave,
  connector_factory: factory, discovery: {directories: [], cwd: output, home: output})
slave.close
master.close
drain.join
raise "Ruby apply failed: #{code}" unless code.zero? && connections == 1 && reads == 2 && writes == plan.fetch('commands')
state = JSON.parse(File.read(Dir[File.join(output, 'ruby-executions', '*', 'execution.json')].fetch(0)))
raise 'Ruby apply readback missing' unless state.fetch('status') == 'configuration_verified'
# The original Ruby validator must also reject an altered Go plan before a session.
tampered = Marshal.load(Marshal.dump(plan)); tampered['commands'] << 'save force'
begin
  Wireless::CLI::Apply.new.send(:validate_plan, tampered)
  raise 'Tampered command accepted'
rescue ArgumentError
  nil
end
puts "Ruby #{RUBY_VERSION} / JSON #{JSON::VERSION}: Go report review, Go plan validation/apply on real PTY, exact full fingerprint/commands, tamper rejection PASS"

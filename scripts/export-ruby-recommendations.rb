# frozen_string_literal: true
# Explicit read-only oracle exporter, stdout only. The full source fixture is
# an original Ruby Config/Snapshot/CLI::Report output; synthetic cases supplement it.
require 'json'
require File.join(ENV.fetch('WIRELESS_RUBY_SOURCE'), 'lib/wireless/recommendations')
raise 'Expected JSON 3.1.0' unless JSON::VERSION == '3.1.0'
def obs(records=[],status='complete');{'status'=>status,'records'=>records,'issues'=>[]};end
def doc(dims,platform='h3c_wireless');{'device'=>{'name'=>'wlc','host'=>'192.0.2.10','platform'=>platform},'snapshot'=>{'dimensions'=>dims}};end
templates=[true,false,nil].map.with_index{|enabled,i|{'name'=>"corp#{i}",'enabled'=>enabled,'configuration'=>{'cipher_suites'=>['ccmp','tkip'],'security_ies'=>['rsn','wpa']}}}
templates << {'name'=>'blocked','enabled'=>true,'configuration'=>{'cipher_suites'=>['tkip'],'security_ies'=>['wpa']}}
config={'service_templates'=>templates,'access_points'=>{'z'=>{'radio'=>{'2'=>{'service_templates'=>[{'name'=>'missing','vlan_id'=>4},{'name'=>'corp1'}]}},'configuration'=>{'service_templates'=>[{'name'=>'second'}]}}},'ap_groups'=>[{'name'=>'g','service_templates'=>[{'name'=>'missing'}]}]}
docs=[doc({'configuration'=>obs([config])}),doc({'radios'=>obs([{'ap_name'=>'a','radio_id'=>2,'admin_status'=>'Down','protocol'=>'ax'}],'partial'),'access_points'=>obs([{'name'=>'a','operational_status'=>'Idle','management_ip'=>'192.0.2.11'}]),'client_ip_conflicts'=>obs([{'ip'=>'192.0.2.1'}]),'configuration'=>obs([],'failed')}),doc({}),doc({},'huawei'),doc({},'h3c'),{'device'=>{'name'=>'x','host'=>'192.0.2.2'},'snapshot'=>nil}]
full=File.expand_path('../testdata/ruby-interop/device.json', __dir__)
docs<<JSON.parse(File.read(full))
docs << doc({'configuration'=>obs([{'service_templates'=>[{'name'=>12,'enabled'=>true,'configuration'=>{'cipher_suites'=>['ccmp','tkip'],'security_ies'=>['rsn','wpa']}},{'name'=>false,'enabled'=>false}], 'access_points'=>{'AP1'=>{'service_templates'=>[{'name'=>12},{'name'=>true}]}}}])})
cases=docs.map{|d|{input_json:JSON.generate(d),advice:Wireless::Recommendations.new(d).call}}
# Retain exponent token spellings as historical input; Ruby first parses the
# numeric value before interpolation. These advisory names never become plans.
%w[1e+3 1e+15 1e-5 -0e+3 5e-324].each do |token|
  raw = %({"device":{"name":"wlc","host":"192.0.2.10","platform":"h3c_wireless"},"snapshot":{"dimensions":{"configuration":{"status":"complete","records":[{"service_templates":[{"name":#{token},"enabled":true,"configuration":{"cipher_suites":["ccmp","tkip"],"security_ies":["rsn","wpa"]}}]}]}}}})
  cases << {input_json:raw,advice:Wireless::Recommendations.new(JSON.parse(raw)).call}
end
raw = '{"device":{"name":"wlc","host":"192.0.2.10","platform":"h3c_wireless"},"snapshot":{"dimensions":{"configuration":{"status":"complete","records":[{"ap_groups":[{"name":1e+3,"service_templates":[{"name":"missing"}]}]}]}}}}'
cases << {input_json:raw,advice:Wireless::Recommendations.new(JSON.parse(raw)).call}
puts JSON.pretty_generate({ruby:RUBY_VERSION,json:JSON::VERSION,cases:cases})

using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Text.Json.Serialization;
using System.Collections.ObjectModel;
using System.Text.Json;

namespace NorthLink;
public static class Json {
 public static readonly JsonSerializerOptions Options=new(){PropertyNamingPolicy=JsonNamingPolicy.CamelCase,PropertyNameCaseInsensitive=true,WriteIndented=false};
}
public sealed class RedisConfig {public string Host{get;set;}="";public int Port{get;set;}=6379;public string Username{get;set;}="";public string Password{get;set;}="";public int Db{get;set;}public bool Tls{get;set;}}
public sealed class ServiceConfig {public string Id{get;set;}=Guid.NewGuid().ToString("N");public string Name{get;set;}="新服务";public string Host{get;set;}="127.0.0.1";public int Port{get;set;}=8080;public bool Enabled{get;set;}=true;public string Display=>$"{Name}  ·  {Host}:{Port}";}
public sealed class MappingConfig:INotifyPropertyChanged {
 public event PropertyChangedEventHandler? PropertyChanged;
 string _runtimeStatus="未应用",_runtimeError="";
 [JsonIgnore] public string RuntimeStatus{get=>_runtimeStatus;set{if(_runtimeStatus!=value){_runtimeStatus=value;PropertyChanged?.Invoke(this,new(nameof(RuntimeStatus)));}}}
 [JsonIgnore] public string RuntimeError{get=>_runtimeError;set{if(_runtimeError!=value){_runtimeError=value;PropertyChanged?.Invoke(this,new(nameof(RuntimeError)));}}}
public string Id{get;set;}=Guid.NewGuid().ToString("N");public string Name{get;set;}="";public string DeviceId{get;set;}="";public string DeviceName{get;set;}="";public string ServiceId{get;set;}="";public string LocalHost{get;set;}="127.0.0.1";public int LocalPort{get;set;}=18080;public bool Enabled{get;set;}=true;}
public sealed class AppConfig {
 public int Version{get;set;}=1;public string Role{get;set;}="provider";public string DeviceId{get;set;}="";public string Name{get;set;}="";public string Group{get;set;}="default";
 public RedisConfig Redis{get;set;}=new();public List<string> Stun{get;set;}=new();public ObservableCollection<ServiceConfig> Services{get;set;}=new();public ObservableCollection<MappingConfig> Mappings{get;set;}=new();public bool AutoConnect{get;set;}public bool StartWithWindows{get;set;}public int ConnectTimeoutSeconds{get;set;}=30;
}
public sealed class Device {public string Id{get;set;}="";public string Name{get;set;}="";public string Role{get;set;}="";public List<ServiceConfig> Services{get;set;}=new();public long UpdatedAt{get;set;}public string Display=>$"{Name}  ·  {Id[..Math.Min(Id.Length,8)]}";}
public sealed class PeerView {public string DeviceId{get;set;}="";public string Name{get;set;}="";public string State{get;set;}="";public string Path{get;set;}="";public string Local{get;set;}="";public string Remote{get;set;}="";public double RttMs{get;set;}public string LastError{get;set;}="";public long Streams{get;set;}public string Latency=>State=="已直连"?$"{RttMs:F1} ms":"—";public string Detail=>string.IsNullOrWhiteSpace(LastError)?Remote:LastError;}
public sealed class MappingView {public string Id{get;set;}="";public string Name{get;set;}="";public string Address{get;set;}="";public string State{get;set;}="";public string LastError{get;set;}="";}
public sealed class LogEntry {public string Time{get;set;}="";public string Level{get;set;}="";public string Message{get;set;}="";public string TimeText=>DateTimeOffset.TryParse(Time,out var d)?d.ToLocalTime().ToString("HH:mm:ss"):Time;public string LevelText=>Level switch{"error"=>"错误","warn"=>"注意",_=>"信息"};public override string ToString()=>$"{Time} [{Level}] {Message}";}
public sealed class CoreState {
 public bool Running{get;set;}public bool SignalOnline{get;set;}public string SignalError{get;set;}="";public string DeviceId{get;set;}="";public string Name{get;set;}="";public string Role{get;set;}="";public string Version{get;set;}="";public bool ServiceMode{get;set;}public List<PeerView> Peers{get;set;}=new();public List<MappingView> Mappings{get;set;}=new();public List<Device> Devices{get;set;}=new();public List<LogEntry> Logs{get;set;}=new();public ulong BytesSent{get;set;}public ulong BytesReceived{get;set;}public long ActiveConnections{get;set;}public long UptimeSeconds{get;set;}
}

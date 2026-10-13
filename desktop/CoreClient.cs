using System;
using System.Collections.Concurrent;
using System.Diagnostics;
using System.IO;
using System.IO.Pipes;
using System.Security.Cryptography;
using System.Security.Principal;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.Win32;

namespace NorthLink;
public sealed class CoreClient:IDisposable {
 public string Role{get;}
 public string PipeName{get;}
 public string ConfigPath{get;}
 public string ExecutablePath=>Path.Combine(AppContext.BaseDirectory,"northlink-core.exe");
 public string ServiceName=>"NorthLink-"+PipeName;
 public bool ServiceInstalled {get{using var key=Registry.LocalMachine.OpenSubKey(@"SYSTEM\CurrentControlSet\Services\"+ServiceName);return key!=null;}}
 NamedPipeClientStream? _pipe;
 StreamWriter? _writer;
 Process? _owned;
 readonly SemaphoreSlim _connectLock=new(1,1),_writeLock=new(1,1);
 readonly ConcurrentDictionary<string,TaskCompletionSource<JsonElement>> _requests=new();
 volatile bool _connected,_disposed;
 public bool IsConnected=>_connected;
 public event Action<CoreState,bool>? StateReceived;
 public event Action? Disconnected;
 public CoreClient(string role){
  Role=role;var sid=WindowsIdentity.GetCurrent().User?.Value??Environment.UserName;
  var hash=Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(sid)))[..12].ToLowerInvariant();
  PipeName=$"NorthLink-{role}-{hash}";
  ConfigPath=Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),"NorthLink",role,"config.json");
 }
 public async Task ConnectAsync(){
  await _connectLock.WaitAsync();try{
   if(_connected||_disposed)return;
   if(await TryPipeAsync(400))return;
   if(ServiceInstalled){if(await TryPipeAsync(5000))return;throw new IOException("无人值守服务尚未就绪。请点击“启动服务”或稍后重试。");}
   if(!File.Exists(ExecutablePath))throw new FileNotFoundException("缺少 northlink-core.exe，请完整解压程序包。");
   var si=new ProcessStartInfo(ExecutablePath){UseShellExecute=false,CreateNoWindow=true,WorkingDirectory=AppContext.BaseDirectory};
   AddCoreArgs(si);_owned=Process.Start(si);
   if(!await TryPipeAsync(12000))throw new IOException("网络核心未就绪，请查看配置目录中的 core.log。");
  }finally{_connectLock.Release();}
 }
 async Task<bool> TryPipeAsync(int timeout){
  var p=new NamedPipeClientStream(".",PipeName,PipeDirection.InOut,PipeOptions.Asynchronous);
  try{await p.ConnectAsync(timeout);}catch{p.Dispose();return false;}
  if(_disposed){p.Dispose();return false;}
  _pipe=p;_writer=new StreamWriter(p,new UTF8Encoding(false),8192,true){AutoFlush=true};_connected=true;
  _=ReadLoop(p);return true;
 }
 async Task ReadLoop(NamedPipeClientStream pipe){
  try{
   using var reader=new StreamReader(pipe,Encoding.UTF8,false,8192,true);
   while(!_disposed){var line=await reader.ReadLineAsync();if(line==null)break;using var doc=JsonDocument.Parse(line);var root=doc.RootElement;
    if(root.GetProperty("type").GetString()=="state"){
     var s=root.GetProperty("state").Deserialize<CoreState>(Json.Options);if(s!=null)StateReceived?.Invoke(s,root.GetProperty("desired").GetBoolean());
    }else if(root.TryGetProperty("id",out var id)&&_requests.TryRemove(id.GetString()??"",out var pending)){pending.TrySetResult(root.Clone());}
   }
  }catch(Exception){}finally{
   if(ReferenceEquals(_pipe,pipe)){_connected=false;foreach(var kv in _requests)if(_requests.TryRemove(kv.Key,out var t))t.TrySetException(new IOException("与网络核心的连接已断开"));Disconnected?.Invoke();}
   pipe.Dispose();
  }
 }
 public async Task<JsonElement> CallAsync(string command,AppConfig? config=null){
  if(!_connected||_writer==null)throw new IOException("网络核心未连接");
  var id=Guid.NewGuid().ToString("N");var t=new TaskCompletionSource<JsonElement>(TaskCreationOptions.RunContinuationsAsynchronously);_requests[id]=t;
  try{
   var json=JsonSerializer.Serialize(new{id,command,config},Json.Options);
   await _writeLock.WaitAsync();try{await _writer.WriteLineAsync(json);}finally{_writeLock.Release();}
   var result=await t.Task.WaitAsync(TimeSpan.FromSeconds(60));
   if(!result.GetProperty("ok").GetBoolean())throw new IOException(result.TryGetProperty("error",out var error)?error.GetString():"操作失败");return result;
  }finally{_requests.TryRemove(id,out _);}
 }
 public async Task DisconnectAsync(bool stopCore){
  if(stopCore&&IsConnected){try{await CallAsync("exit");}catch{}}
  _connected=false;_pipe?.Dispose();_pipe=null;_writer=null;
  if(stopCore&&_owned!=null){try{await _owned.WaitForExitAsync().WaitAsync(TimeSpan.FromSeconds(20));}catch{try{_owned.Kill(true);}catch{}}_owned.Dispose();_owned=null;}
 }
 public async Task ElevatedActionAsync(string action){
  var si=new ProcessStartInfo(ExecutablePath){UseShellExecute=true,Verb="runas",WorkingDirectory=AppContext.BaseDirectory};AddCoreArgs(si);si.ArgumentList.Add("--service-action");si.ArgumentList.Add(action);
  using var p=Process.Start(si)??throw new IOException("无法启动管理员操作");await p.WaitForExitAsync();
  if(p.ExitCode!=0){var file=Path.Combine(Path.GetDirectoryName(ConfigPath)!,"operation-error.txt");throw new IOException(File.Exists(file)?await File.ReadAllTextAsync(file):"操作失败或未获得管理员权限");}
 }
 void AddCoreArgs(ProcessStartInfo si){si.ArgumentList.Add("--role");si.ArgumentList.Add(Role);si.ArgumentList.Add("--config");si.ArgumentList.Add(ConfigPath);si.ArgumentList.Add("--pipe");si.ArgumentList.Add(PipeName);}
 public void Dispose(){_disposed=true;_connected=false;_pipe?.Dispose();_owned?.Dispose();}
}

using System;
using System.IO;
using System.Threading;
using System.Windows;
using Application=System.Windows.Application;
namespace NorthLink;
public partial class App:Application {
 Mutex? _mutex;EventWaitHandle? _showEvent;RegisteredWaitHandle? _wait;
 protected override void OnStartup(StartupEventArgs e){
  base.OnStartup(e);var role=Path.GetFileNameWithoutExtension(Environment.ProcessPath??"").Contains("Client",StringComparison.OrdinalIgnoreCase)?"client":"provider";
  for(int i=0;i<e.Args.Length-1;i++)if(e.Args[i]=="--role")role=e.Args[i+1]=="client"?"client":"provider";
  var core=new CoreClient(role);_mutex=new Mutex(true,@"Local\"+core.PipeName+"-ui",out var created);
  if(!created){try{using var ev=EventWaitHandle.OpenExisting(@"Local\"+core.PipeName+"-show");ev.Set();}catch{}core.Dispose();Shutdown();return;}
  var window=new MainWindow(core,Array.IndexOf(e.Args,"--minimized")>=0);MainWindow=window;
  _showEvent=new EventWaitHandle(false,EventResetMode.AutoReset,@"Local\"+core.PipeName+"-show");
  _wait=ThreadPool.RegisterWaitForSingleObject(_showEvent,(_,_)=>Dispatcher.BeginInvoke(new Action(window.RestoreWindow)),null,-1,false);
  DispatcherUnhandledException+=(_,args)=>{System.Windows.MessageBox.Show(args.Exception.Message,"NorthLink",MessageBoxButton.OK,MessageBoxImage.Error);args.Handled=true;};
  window.Show();
 }
 protected override void OnExit(ExitEventArgs e){_wait?.Unregister(null);_showEvent?.Dispose();_mutex?.Dispose();base.OnExit(e);}
}

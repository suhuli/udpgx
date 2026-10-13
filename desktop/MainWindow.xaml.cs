using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Text.Json;
using System.Threading.Tasks;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Threading;
using Microsoft.Win32;
using Forms=System.Windows.Forms;
using MessageBox=System.Windows.MessageBox;

namespace NorthLink;
public partial class MainWindow:Window {
 readonly CoreClient _core;readonly bool _minimized;readonly bool _provider;
 AppConfig? _config;CoreState _state=new();bool _desired,_busy,_exit,_balloonShown;
 readonly Forms.NotifyIcon _tray;readonly Forms.ToolStripMenuItem _keepServiceItem;
 readonly DispatcherTimer _reconnect;
 ulong _lastSent,_lastReceived;DateTime _lastSample=DateTime.UtcNow;
 string _deviceSignature="";bool _configLoaded;
 static readonly string[] DefaultStun={"stun:stun.hitv.com:3478","stun:stun.miwifi.com:3478","stun:stun.xten.com:3478","stun:stun.voip.aebc.com:3478"};
 public MainWindow(CoreClient core,bool minimized){
  InitializeComponent();_core=core;_minimized=minimized;_provider=core.Role=="provider";
  Title="NorthLink · "+(_provider?"提供端":"客户端");SidebarMode.Text=_provider?"提供端 / PROVIDER":"客户端 / CLIENT";
  NavServiceLabel.Text=_provider?"服务管理":"端口映射";
  if(!_provider){DiscoveryPanel.Visibility=Visibility.Visible;MappingsGrid.Visibility=Visibility.Visible;ServicesGrid.Visibility=Visibility.Collapsed;AddServiceButton.Visibility=Visibility.Collapsed;ServiceModePanel.Visibility=Visibility.Collapsed;ServiceHint.Text="先启动信令，选择在线提供端与服务，添加本地映射。保存并应用后自动预建直连。";EmptyPeerHint.Text="启动连接，在“端口映射”中添加提供端的服务。";}
  _tray=new Forms.NotifyIcon{Text=Title,Visible=true};
  try{_tray.Icon=new System.Drawing.Icon(Path.Combine(AppContext.BaseDirectory,"northlink.ico"));}catch{_tray.Icon=System.Drawing.SystemIcons.Application;}
  _tray.DoubleClick+=(_,_)=>Dispatcher.BeginInvoke(new Action(RestoreWindow));
  var menu=new Forms.ContextMenuStrip();
  menu.Items.Add("打开主界面",null,(_,_)=>Dispatcher.BeginInvoke(new Action(RestoreWindow)));
  menu.Items.Add("连接配置",null,(_,_)=>Dispatcher.BeginInvoke(new Action(()=>{RestoreWindow();Navigate(2);})));
  menu.Items.Add(new Forms.ToolStripSeparator());
  menu.Items.Add("启动连接",null,(_,_)=>Dispatcher.BeginInvoke(new Action(async()=>await ExecuteAsync(StartAsync))));
  menu.Items.Add("停止连接",null,(_,_)=>Dispatcher.BeginInvoke(new Action(async()=>await ExecuteAsync(async()=>{await EnsureConnectedAsync();await _core.CallAsync("stop");}))));
  menu.Items.Add(new Forms.ToolStripSeparator());
  _keepServiceItem=new Forms.ToolStripMenuItem("退出界面（保留无人值守服务）",null,(_,_)=>Dispatcher.BeginInvoke(new Action(async()=>await ExitAsync(false)))){Visible=false};menu.Items.Add(_keepServiceItem);
  menu.Items.Add("停止并退出",null,(_,_)=>Dispatcher.BeginInvoke(new Action(async()=>await ExitAsync(true))));_tray.ContextMenuStrip=menu;
  _core.StateReceived+=(s,wanted)=>Dispatcher.BeginInvoke(new Action(()=>Render(s,wanted)));
  _core.Disconnected+=()=>Dispatcher.BeginInvoke(new Action(()=>{if(_exit)return;FooterStatus.Text="本机网络核心已断开，正在尝试恢复…";SignalStatusText.Text="核心离线";}));
  _reconnect=new DispatcherTimer{Interval=TimeSpan.FromSeconds(5)};_reconnect.Tick+=async(_,_)=>{
   if(_busy||_exit||_core.IsConnected)return;
   try{await _core.ConnectAsync();if(!_configLoaded)await LoadConfigAsync();else if(_desired&&!_state.ServiceMode)await _core.CallAsync("start");}catch{}
  };
  Loaded+=async(_,_)=>{
   Height=Math.Min(Height,Math.Max(660,SystemParameters.WorkArea.Height-30));Width=Math.Min(Width,SystemParameters.WorkArea.Width-30);
   Navigate(0);if(_minimized)Hide();
   await ExecuteAsync(async()=>{await _core.ConnectAsync();await LoadConfigAsync();if(string.IsNullOrWhiteSpace(_config!.Redis.Host)){Navigate(2);Notice("先填写 Redis 地址与密码。两端使用相同数据库和连接组，STUN 已预置。",false);}});_reconnect.Start();
  };
 }
 protected override void OnClosing(CancelEventArgs e){if(!_exit){e.Cancel=true;Hide();if(!_balloonShown){_tray.ShowBalloonTip(2000,"NorthLink 正在后台运行","右下角托盘可打开、配置、停止或退出。",Forms.ToolTipIcon.Info);_balloonShown=true;}}base.OnClosing(e);}
 public void RestoreWindow(){Show();WindowState=WindowState.Normal;Activate();}
 async Task ExitAsync(bool stopCore){
  if(_exit||_busy)return;
  if(stopCore&&_state.ActiveConnections>0&&MessageBox.Show(this,"退出将断开正在使用的连接。确定继续？","NorthLink",MessageBoxButton.YesNo,MessageBoxImage.Question)!=MessageBoxResult.Yes)return;
  _exit=true;_reconnect.Stop();IsEnabled=false;
  await _core.DisconnectAsync(stopCore);_core.Dispose();_tray.Visible=false;_tray.Dispose();System.Windows.Application.Current.Shutdown();
 }
 void Navigate(int page){
  Pages.SelectedIndex=page;
  var navs=new[]{NavOverview,NavServices,NavSettings,NavLogs};for(var i=0;i<navs.Length;i++)navs[i].Background=i==page?Brush("#213D3D"):Brushes.Transparent;
  PageTitle.Text=page switch{0=>"连接工作台",1=>_provider?"服务管理":"端口映射",2=>"连接配置",_=>"运行日志"};
  PageSubtitle.Text=page switch{0=>_provider?"把本地服务，直接分享给另一端。":"通过本地端口，访问另一端的服务。",1=>_provider?"只发布你选择的 TCP 目标。":"一条映射，让应用像访问本机一样连接。",2=>"两端的起点，在这里配置。",_=>"每一步连接，都有迹可循。"};
 }
 static SolidColorBrush Brush(string hex)=>(SolidColorBrush)new BrushConverter().ConvertFromString(hex)!;
 void Nav_Click(object sender,RoutedEventArgs e)=>Navigate(int.Parse((string)((Button)sender).Tag));
 void DismissNotice_Click(object sender,RoutedEventArgs e)=>NoticeBar.Visibility=Visibility.Collapsed;
 void Notice(string text,bool error){NoticeText.Text=text;NoticeBar.Background=Brush(error?"#3A2826":"#21372F");NoticeBar.BorderBrush=Brush(error?"#69443A":"#365A48");NoticeText.Foreground=Brush(error?"#F1B6A0":"#B7DBBA");NoticeBar.Visibility=Visibility.Visible;}
 async Task ExecuteAsync(Func<Task> action){
  if(_busy||_exit)return;_busy=true;StartButton.IsEnabled=false;StopButton.IsEnabled=false;
  try{await action();}catch(Exception ex){Notice(ex.Message,true);}finally{_busy=false;UpdateButtons();}
 }
 async Task EnsureConnectedAsync(){if(!_core.IsConnected){await _core.ConnectAsync();if(!_configLoaded)await LoadConfigAsync();}}
 async Task LoadConfigAsync(){var reply=await _core.CallAsync("get_config");_config=reply.GetProperty("config").Deserialize<AppConfig>(Json.Options)??throw new IOException("配置读取失败");FillConfig();_configLoaded=true;}
 void FillConfig(){
  if(_config==null)return;var c=_config;
  RedisHostBox.Text=c.Redis.Host;RedisPortBox.Text=c.Redis.Port.ToString();RedisDbBox.Text=c.Redis.Db.ToString();RedisUserBox.Text=c.Redis.Username;RedisPasswordBox.Password=c.Redis.Password;RedisTlsCheck.IsChecked=c.Redis.Tls;
  DeviceNameBox.Text=c.Name;GroupBox.Text=c.Group;TimeoutBox.Text=c.ConnectTimeoutSeconds.ToString();AutoConnectCheck.IsChecked=c.AutoConnect;StartupCheck.IsChecked=c.StartWithWindows;StunBox.Text=string.Join(Environment.NewLine,c.Stun);DeviceIdText.Text="ID  "+c.DeviceId;
  ServicesGrid.ItemsSource=c.Services;MappingsGrid.ItemsSource=c.Mappings;
 }
 void CaptureConfig(){
  if(_config==null)throw new InvalidOperationException("配置尚未加载");
  if(!ServicesGrid.CommitEdit(DataGridEditingUnit.Cell,true)||!ServicesGrid.CommitEdit(DataGridEditingUnit.Row,true)||!MappingsGrid.CommitEdit(DataGridEditingUnit.Cell,true)||!MappingsGrid.CommitEdit(DataGridEditingUnit.Row,true))throw new InvalidOperationException("表格中有无效输入，请检查端口等字段。");
  static int Number(string text,string label){if(!int.TryParse(text.Trim(),out int value))throw new InvalidOperationException(label+"必须为整数");return value;}
  var c=_config;c.Redis.Host=RedisHostBox.Text.Trim();c.Redis.Port=Number(RedisPortBox.Text,"Redis 端口");c.Redis.Db=Number(RedisDbBox.Text,"Redis DB");c.Redis.Username=RedisUserBox.Text.Trim();c.Redis.Password=RedisPasswordBox.Password;c.Redis.Tls=RedisTlsCheck.IsChecked==true;
  c.Name=DeviceNameBox.Text.Trim();c.Group=GroupBox.Text.Trim();c.ConnectTimeoutSeconds=Number(TimeoutBox.Text,"直连超时");c.AutoConnect=AutoConnectCheck.IsChecked==true;c.StartWithWindows=StartupCheck.IsChecked==true;
  c.Stun=StunBox.Text.Split(new[]{'\r','\n'},StringSplitOptions.RemoveEmptyEntries).Select(s=>s.Trim()).Where(s=>s.Length>0).Select(s=>s.StartsWith("stun:",StringComparison.Ordinal)?s:"stun:"+s+(s.Contains(':')?"":":3478")).Distinct().ToList();
 }
 async Task SaveConfigAsync(){
  await EnsureConnectedAsync();CaptureConfig();await _core.CallAsync("save",_config);SetStartup();
 }
 void SetStartup(){
  using var key=Registry.CurrentUser.CreateSubKey(@"Software\Microsoft\Windows\CurrentVersion\Run");var name="NorthLink."+_core.Role;
  if(_config!.StartWithWindows)key.SetValue(name,$"\"{Environment.ProcessPath}\" --role {_core.Role} --minimized");else key.DeleteValue(name,false);
 }
 async Task StartAsync(){await SaveConfigAsync();await _core.CallAsync("start");NoticeBar.Visibility=Visibility.Collapsed;Navigate(0);}
 async void Start_Click(object sender,RoutedEventArgs e)=>await ExecuteAsync(StartAsync);
 async void Stop_Click(object sender,RoutedEventArgs e)=>await ExecuteAsync(async()=>{await EnsureConnectedAsync();await _core.CallAsync("stop");Notice("已停止，本地监听与直连已释放。",false);});
 async void Save_Click(object sender,RoutedEventArgs e)=>await ExecuteAsync(async()=>{await SaveConfigAsync();Notice("已保存并应用。运行中的网络连接已按新配置重新建立。",false);});
 void UpdateButtons(){StartButton.IsEnabled=!_busy;StopButton.IsEnabled=!_busy&&_desired;StartButton.Content=_desired?"重新连接":"启动连接";bool installed=false;try{installed=_core.ServiceInstalled;}catch{}InstallServiceButton.IsEnabled=!installed&&!_busy;RemoveServiceButton.IsEnabled=installed&&!_busy;StartServiceButton.IsEnabled=installed&&!_busy;ServiceModeHint.Text=installed?"已注册 Windows 服务 · 重启后自动启动（无需登录）":"尚未安装服务 · 当前按登录用户运行";}
 void Render(CoreState s,bool desired){
  if(_exit)return;_state=s;_desired=desired;
  SignalStatusText.Text=s.SignalOnline?"已连接":s.Running?"重连中":desired?"正在重试":"未启动";
  SignalStatusText.Foreground=s.SignalOnline?Brush("#85E6C5"):Brush("#E0C28E");
  SignalDetailText.Text=s.SignalOnline?"只交换状态与连接信息":string.IsNullOrEmpty(s.SignalError)?"等待启动信令":s.SignalError;SignalDetailText.ToolTip=s.SignalError;
  PeerCountText.Text=s.Peers.Count(p=>p.State=="已直连")+" 台";StreamsText.Text=s.ActiveConnections+" 条业务连接";
  var now=DateTime.UtcNow;var elapsed=Math.Max(.1,(now-_lastSample).TotalSeconds);var up=s.BytesSent>=_lastSent?(s.BytesSent-_lastSent)/elapsed:0;var down=s.BytesReceived>=_lastReceived?(s.BytesReceived-_lastReceived)/elapsed:0;
  RateText.Text=Bytes(up+down)+"/s";RateDetailText.Text=$"↑ {Bytes(up)}/s    ↓ {Bytes(down)}/s";_lastSent=s.BytesSent;_lastReceived=s.BytesReceived;_lastSample=now;
  PeersGrid.ItemsSource=s.Peers;EmptyPeers.Visibility=s.Peers.Count==0?Visibility.Visible:Visibility.Collapsed;
  LogsGrid.ItemsSource=s.Logs??new List<LogEntry>();
  RunModeText.Text=s.ServiceMode?"Windows 服务 · 无人值守":"托盘后台模式";
  _keepServiceItem.Visible=s.ServiceMode;
  FooterStatus.Text=s.Running?$"{s.Name}  ·  已运行 {TimeSpan.FromSeconds(s.UptimeSeconds):hh\\:mm\\:ss}  ·  ↑ {Bytes(s.BytesSent)}  ↓ {Bytes(s.BytesReceived)}":desired?"正在尝试启动，失败会自动重试；可点击停止取消。":"已停止 · 在连接配置中保存设置后启动。";
  _tray.Text=(Title+" | "+(s.Running?$"直连 {s.Peers.Count(p=>p.State=="已直连")} 台":"未启动"))[..Math.Min(63,(Title+" | "+(s.Running?$"直连 {s.Peers.Count(p=>p.State=="已直连")} 台":"未启动")).Length)];
  if(!_provider){
   var providers=s.Devices.Where(d=>d.Role=="provider").ToList();var signature=string.Join("|",providers.Select(d=>d.Id+":"+d.Name+":"+string.Join(",",d.Services.Select(v=>v.Id+v.Name+v.Port))));
   if(signature!=_deviceSignature){_deviceSignature=signature;var selected=(DeviceCombo.SelectedItem as Device)?.Id;DeviceCombo.ItemsSource=providers;DeviceCombo.SelectedItem=providers.FirstOrDefault(d=>d.Id==selected)??providers.FirstOrDefault();}
   if(_config!=null)foreach(var m in _config.Mappings){var runtime=s.Mappings.FirstOrDefault(v=>v.Id==m.Id);m.RuntimeStatus=runtime?.State??(s.Running?"未应用":"未启动");m.RuntimeError=runtime?.LastError??"";}
   MappingFooter.Text=s.Mappings.Any(m=>m.State=="监听失败")?"有本地端口监听失败，请在运行日志中查看端口占用详情。":"默认仅监听 127.0.0.1。编辑后保存，现有会话会断开。";
  }
  UpdateButtons();
 }
 static string Bytes(double n){string[] units={"B","KB","MB","GB","TB"};int i=0;while(n>=1024&&i<units.Length-1){n/=1024;i++;}return i==0?$"{n:F0} {units[i]}":$"{n:F1} {units[i]}";}
 void DeviceCombo_SelectionChanged(object sender,SelectionChangedEventArgs e){if(ServiceCombo==null)return;var old=(ServiceCombo.SelectedItem as ServiceConfig)?.Id;var services=(DeviceCombo.SelectedItem as Device)?.Services??new List<ServiceConfig>();ServiceCombo.ItemsSource=services;ServiceCombo.SelectedItem=services.FirstOrDefault(s=>s.Id==old)??services.FirstOrDefault();}
 void AddService_Click(object sender,RoutedEventArgs e){if(_config==null)return;var item=new ServiceConfig();_config.Services.Add(item);ServicesGrid.SelectedItem=item;ServicesGrid.ScrollIntoView(item);}
 void AddMapping_Click(object sender,RoutedEventArgs e){
  if(_config==null)return;if(DeviceCombo.SelectedItem is not Device d||ServiceCombo.SelectedItem is not ServiceConfig s){Notice("请先启动连接，等待在线提供端出现，再选择服务。",true);return;}
  int port=s.Port==80?18080:s.Port==3389?13389:s.Port;while(_config.Mappings.Any(m=>m.LocalPort==port)&&port<65535)port++;
  var m=new MappingConfig{Name=s.Name,DeviceId=d.Id,DeviceName=d.Name,ServiceId=s.Id,LocalPort=port};_config.Mappings.Add(m);MappingsGrid.SelectedItem=m;MappingsGrid.ScrollIntoView(m);Notice($"已添加草稿：127.0.0.1:{port} → {d.Name} / {s.Name}。点击“保存并应用”开始监听。",false);
 }
 void DeleteRow_Click(object sender,RoutedEventArgs e){if(_config==null)return;if(_provider&&ServicesGrid.SelectedItem is ServiceConfig s)_config.Services.Remove(s);else if(!_provider&&MappingsGrid.SelectedItem is MappingConfig m)_config.Mappings.Remove(m);}
 void ResetStun_Click(object sender,RoutedEventArgs e)=>StunBox.Text=string.Join(Environment.NewLine,DefaultStun);
 async void Firewall_Click(object sender,RoutedEventArgs e)=>await ExecuteAsync(async()=>{await _core.ElevatedActionAsync("firewall");Notice("已添加本程序 UDP 入站规则。企业网络或路由器的限制仍需单独处理。",false);});
 async void InstallService_Click(object sender,RoutedEventArgs e)=>await ExecuteAsync(async()=>{
  await SaveConfigAsync();if(string.IsNullOrWhiteSpace(_config!.Redis.Host))throw new InvalidOperationException("请先填写并保存 Redis 配置。");
  if(MessageBox.Show(this,"安装服务需要管理员权限。安装后核心将在未登录 Windows 时运行，请勿移动程序目录。安装过程会断开现有连接。继续？","无人值守运行",MessageBoxButton.YesNo,MessageBoxImage.Question)!=MessageBoxResult.Yes)return;
  await _core.DisconnectAsync(true);try{await _core.ElevatedActionAsync("install");}finally{await _core.ConnectAsync();}await LoadConfigAsync();Notice("无人值守服务已安装并启动。可在托盘中仅退出界面、保留服务。",false);
 });
 async void StartService_Click(object sender,RoutedEventArgs e)=>await ExecuteAsync(async()=>{await _core.ElevatedActionAsync("start");await _core.ConnectAsync();if(!_configLoaded)await LoadConfigAsync();Notice("已请求启动无人值守服务。",false);});
 async void RemoveService_Click(object sender,RoutedEventArgs e)=>await ExecuteAsync(async()=>{
  if(MessageBox.Show(this,"移除无人值守服务会断开现有连接，并恢复普通托盘模式。继续？","NorthLink",MessageBoxButton.YesNo,MessageBoxImage.Question)!=MessageBoxResult.Yes)return;
  await _core.DisconnectAsync(false);await _core.ElevatedActionAsync("remove");await _core.ConnectAsync();await LoadConfigAsync();Notice("已移除无人值守服务。现在按当前登录用户运行。",false);
 });
 void OpenConfigFolder_Click(object sender,RoutedEventArgs e){Directory.CreateDirectory(Path.GetDirectoryName(_core.ConfigPath)!);Process.Start(new ProcessStartInfo("explorer.exe",Path.GetDirectoryName(_core.ConfigPath)!){UseShellExecute=true});}
 async void Export_Click(object sender,RoutedEventArgs e)=>await ExecuteAsync(async()=>{
  CaptureConfig();if(MessageBox.Show(this,"导出文件包含 Redis 密码。请妥善保存，不要公开发送。继续？","导出配置",MessageBoxButton.YesNo,MessageBoxImage.Warning)!=MessageBoxResult.Yes)return;
  var dlg=new SaveFileDialog{Filter="JSON 配置|*.json",FileName="northlink-"+_core.Role+".json"};if(dlg.ShowDialog(this)==true)await File.WriteAllTextAsync(dlg.FileName,JsonSerializer.Serialize(_config,new JsonSerializerOptions(Json.Options){WriteIndented=true}));
 });
 async void Import_Click(object sender,RoutedEventArgs e)=>await ExecuteAsync(async()=>{
  await EnsureConnectedAsync();var dlg=new OpenFileDialog{Filter="JSON 配置|*.json"};if(dlg.ShowDialog(this)!=true)return;
  var c=JsonSerializer.Deserialize<AppConfig>(await File.ReadAllTextAsync(dlg.FileName),Json.Options)??throw new IOException("配置无效");if(c.Role!=_core.Role)throw new IOException("配置角色与当前程序不一致");c.DeviceId=_config!.DeviceId;_config=c;FillConfig();Notice("配置已导入为草稿，并保留本机唯一设备 ID。检查后点击保存并应用。",false);
 });
 async void ExportLogs_Click(object sender,RoutedEventArgs e)=>await ExecuteAsync(async()=>{var dlg=new SaveFileDialog{Filter="文本日志|*.txt",FileName="northlink-log-"+DateTime.Now.ToString("yyyyMMdd-HHmmss")+".txt"};if(dlg.ShowDialog(this)==true)await File.WriteAllLinesAsync(dlg.FileName,(_state.Logs??new()).Select(l=>l.ToString()));});
}

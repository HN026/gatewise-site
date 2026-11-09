import argparse,json,pathlib,subprocess,tempfile
parser=argparse.ArgumentParser();parser.add_argument("--version",default="v1.0.0");parser.add_argument("--candidate",type=pathlib.Path);args=parser.parse_args()
p=pathlib.Path(tempfile.mkdtemp(prefix='gatewise-v1-consumer-'))
(p/'main.go').write_text('''package main
import("context";"database/sql";"database/sql/driver";"errors";"fmt";"io";"net/http";"net/http/httptest";"github.com/HN026/gatewise/operation";"github.com/HN026/gatewise/sqlguard";"github.com/HN026/gatewise/regulator";"github.com/HN026/gatewise/postgres";"github.com/HN026/gatewise/middleware";"github.com/HN026/gatewise/metrics";"github.com/prometheus/client_golang/prometheus";"time")
type connector struct{}
func(connector)Connect(context.Context)(driver.Conn,error){return conn{},nil}
func(connector)Driver()driver.Driver{return drv{}}
type drv struct{}
func(drv)Open(string)(driver.Conn,error){return conn{},nil}
type conn struct{}
func(conn)Prepare(string)(driver.Stmt,error){return nil,errors.New("unused")}
func(conn)Close()error{return nil}
func(conn)Begin()(driver.Tx,error){return tx{},nil}
func(conn)ExecContext(context.Context,string,[]driver.NamedValue)(driver.Result,error){return driver.RowsAffected(1),nil}
func(conn)QueryContext(context.Context,string,[]driver.NamedValue)(driver.Rows,error){return &rows{},nil}
type tx struct{}
func(tx)Commit()error{return nil}
func(tx)Rollback()error{return nil}
type rows struct{n int}
func(*rows)Columns()[]string{return []string{"value"}}
func(*rows)Close()error{return nil}
func(r *rows)Next(dest []driver.Value)error{if r.n>0{return io.EOF};r.n++;dest[0]=int64(7);return nil}
func main(){
 engine,err:=operation.New(operation.Config{Name:"sql",MaxConcurrent:1,Queue:&operation.QueueConfig{MaxWaiting:2,MaxWait:20*time.Millisecond}});if err!=nil{panic(err)}
 ctx,cancel:=context.WithCancel(context.Background());permit,err:=engine.Begin(ctx,operation.Request{});if err!=nil{panic(err)};cancel();if engine.Snapshot().Active!=1{panic("premature cancel release")};permit.Finish(operation.Outcome{});permit.Finish(operation.Outcome{});if engine.Snapshot().Active!=0{panic("permit leak")}
 if err=engine.Do(context.Background(),operation.Request{},func(c context.Context)error{_,e:=engine.Begin(c,operation.Request{});if !errors.Is(e,operation.ErrNested){panic("nested admission accepted")};return nil});err!=nil{panic(err)}
 raw:=sql.OpenDB(connector{});defer raw.Close();raw.SetMaxOpenConns(1)
 guarded,err:=sqlguard.New(raw,engine,func(context.Context)operation.Request{return operation.Request{Resource:"database"}});if err!=nil{panic(err)}
 stream,err:=guarded.QueryContext(context.Background(),"stream");if err!=nil{panic(err)};if engine.Snapshot().Active!=1{panic("stream released")};
 deadline,stop:=context.WithTimeout(context.Background(),5*time.Millisecond);_,waitErr:=guarded.QueryContext(deadline,"blocked");stop();if !errors.Is(waitErr,context.DeadlineExceeded)||engine.Snapshot().Active!=1||engine.QueueSnapshot().Waiting!=0{panic("queued SQL deadline accounting")};
 if _,err=engine.Begin(context.Background(),operation.Request{NoWait:true});err==nil{panic("native NoWait bypassed capacity")};if !stream.Next(){panic("no row")};var n int;if stream.Scan(&n)!=nil||n!=7{panic("bad row")};if err=stream.Close();err!=nil{panic(err)}
 if err=guarded.InTx(context.Background(),nil,func(t *sqlguard.Tx)error{_,err:=t.ExecContext(t.Context(),"write");return err});err!=nil{panic(err)}
 if engine.Snapshot().Active!=0{panic("SQL leaked")};if o,err:=(regulator.SQLPool{DB:raw}).Sample(context.Background());err!=nil||o.Value!=0{panic("pool")};if _,err=postgres.NewReplication(postgres.Config{DB:raw,ApplicationName:"standby",StaleAfter:time.Second});err!=nil{panic(err)}
 contention,err:=regulator.NewSQLPoolContention(regulator.SQLPoolContentionConfig{DB:raw});if err!=nil{panic(err)};if o,e:=contention.Sample(context.Background());e!=nil||o.Value!=0||contention.Snapshot().Pressure{panic("contention provider baseline")};
 registry:=prometheus.NewRegistry();if err=metrics.Register(registry,engine);err!=nil{panic(err)};if _,err=registry.Gather();err!=nil{panic(err)}
 guard,err:=middleware.NewService(middleware.ServiceConfig{Name:"http",MaxConcurrent:1});if err!=nil{panic(err)};h:=guard.Middleware(middleware.Critical,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){middleware.InjectCost(r);if r.Header.Get(middleware.CostHeader)!=""{panic("unresolved cost forwarded")};w.WriteHeader(204)}));r:=httptest.NewRequest("POST","/",nil);r.Header.Set(middleware.CostHeader,`{"write_bytes":123}`);w:=httptest.NewRecorder();h.ServeHTTP(w,r);if w.Code!=204{panic(w.Code)}
 stable,err:=middleware.NewService(middleware.ServiceConfig{Name:"stable",MaxConcurrent:8,ReservedCritical:1,Queue:&middleware.QueueConfig{MaxWaiting:4,MaxWait:20*time.Millisecond},Adaptive:&middleware.AdaptiveConfig{Profile:"stable",MinSamples:2,Interval:time.Millisecond}});if err!=nil{panic(err)}
 for i:=0;i<4;i++{req:=httptest.NewRequest("GET","/stable",nil);out:=httptest.NewRecorder();stable.Middleware(middleware.Critical,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){time.Sleep(2*time.Millisecond);w.WriteHeader(204)})).ServeHTTP(out,req);if out.Code!=204||out.Header().Get("X-Gatewise-Controller")!="stable"||out.Header().Get("X-Gatewise-Queue-Eligible")!="true"{panic("profile headers")}}
 snap:=stable.ControllerSnapshot();if snap.Profile!="stable"||snap.EvaluatedAt.IsZero()||snap.ControlRatio==0||snap.WindowInterval<=0{panic("profile feedback")}
 reg:=prometheus.NewRegistry();if metrics.Register(reg,stable)!=nil{panic("stable registry")};families,err:=reg.Gather();if err!=nil{panic(err)};found,queued:=false,false;for _,f:=range families{if f.GetName()=="gatewise_controller_info"{found=true};if f.GetName()=="gatewise_queue_info"{queued=true}};if !found||!queued{panic("profile/queue metric missing")}
 if _,err=middleware.NewService(middleware.ServiceConfig{MaxConcurrent:8,Adaptive:&middleware.AdaptiveConfig{Profile:"invalid"}});err==nil{panic("invalid profile accepted")}
 native,err:=operation.New(operation.Config{Name:"adaptive-native",MaxConcurrent:8,ReservedCritical:1,Adaptive:&operation.AdaptiveConfig{Profile:"stable"}});if err!=nil||native.ControllerSnapshot().Profile!="stable"{panic("native profile")}
 fmt.Println("PASS integration: operation lifetimes, nesting, SQL streams/transactions, providers, metrics and HTTP compatibility")
}
''')
subprocess.run(['go','mod','init','example.com/published-operation-consumer'],cwd=p,check=True)
if args.candidate:
 subprocess.run(['go','mod','edit','-replace','github.com/HN026/gatewise='+str(args.candidate.resolve())],cwd=p,check=True)
for command in [['go','get','github.com/HN026/gatewise@'+args.version],['go','mod','tidy'],['go','run','.']]:subprocess.run(command,cwd=p,check=True)
module=json.loads(subprocess.check_output(['go','list','-m','-json','github.com/HN026/gatewise'],cwd=p,text=True))
assert module['Version']==args.version
if args.candidate:assert 'Replace' in module;print('PASS candidate integration from isolated consumer module; not a public module-download check')
else:assert 'Replace' not in module;print('PASS public module download without local replace')

config=p/'proxy.json';config.write_text(json.dumps({'listen_addr':'127.0.0.1:8080','read_timeout':'1s','services':{'orders':{'upstream_url':'http://127.0.0.1:9090','max_concurrent':4,'reserved_critical':1,'queue':{'max_waiting':4,'max_wait':'20ms','reserved':{'critical':1}}}},'routes':[{'path_prefix':'/','service':'orders'}]}))
subprocess.run(['go','run','github.com/HN026/gatewise/cmd/gatewise','-check-config','-config',str(config),'-env-file',''],cwd=p,check=True)
print('PASS bounded SQL waiting, native opt-out, HTTP eligibility, queue metrics and proxy validation')

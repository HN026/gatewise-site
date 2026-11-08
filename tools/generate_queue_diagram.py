from prepare_assets import diagram
diagram('bounded-waiting',{
 'request':(15,30,205,70,'Prepared request','Trusted identity + bounded body',False),
 'attempt':(295,30,215,70,'Admission checks','Signals / resources / tenant',True),
 'execute':(845,30,205,70,'Execute work','Hold permits until completion',False),
 'queue':(295,225,215,70,'Bounded waiting','Priority + FIFO + aging',True),
 'recheck':(575,225,205,70,'Selected request','Recheck all hard rules',True),
 'reject':(15,225,205,70,'Reject / cancel','Full / expired / caller done',False),
},[('request','attempt',''),('attempt','execute','capacity'),('attempt','queue','local capacity full'),('queue','recheck','turn'),('recheck','execute','admitted'),('queue','reject','bound'),('recheck','reject','policy veto')],1080,390)

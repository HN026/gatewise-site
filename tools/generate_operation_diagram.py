from prepare_assets import diagram
diagram('operation-lifetimes',{
 'http':(15,130,195,70,'HTTP budget','Until handler returns',True),
 'worker':(15,265,195,70,'Worker budget','Until job callback ends',True),
 'sql':(315,200,230,70,'Primary SQL budget','Rows close / transaction ends',True),
 'read':(315,65,230,70,'Follower read budget','Until read rows close',True),
 'primary':(650,200,220,70,'Primary PostgreSQL','Bounded connection pool',False),
 'replica':(650,65,220,70,'Physical standby','Actual streaming replication',False),
 'observer':(650,340,220,70,'Primary-side observer','Pool use + WAL byte lag',False),
}, [('http','sql',''),('worker','sql',''),('http','read',''),('sql','primary',''),('read','replica',''),('primary','observer','')],900,430)

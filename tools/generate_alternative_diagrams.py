"""Original explanatory models; emit matching SVG and editable draw.io files."""
from prepare_assets import diagram

# Models illustrate policy choices, not measured traffic or a product implementation.
diagram('token-bucket', {
 'arrivals': (20,25,190,70,'Incoming requests','Steady traffic or a burst',False),
 'bucket': (305,25,190,70,'Token bucket','Refill 120/s; burst 20',True),
 'work': (600,25,190,70,'Application handlers','Duration may change',False),
 'reject': (305,165,190,70,'No token available','Reject by rate policy',False),
}, [('arrivals','bucket',''),('bucket','work','token available'),('bucket','reject','')],810,260)

diagram('shared-quota', {
 'replica_a': (20,25,220,70,'Service replica A','Check shared allowance',False),
 'replica_b': (20,165,220,70,'Service replica B','Check shared allowance',False),
 'coordinator': (530,95,250,70,'Shared quota store','Counter / allow-or-deny decision',True),
}, [('replica_a','coordinator','quota lookup'),('replica_b','coordinator','quota lookup')],810,260)

diagram('fifo-queue', {
 'arrival': (15,60,105,70,'Arrival','Join tail',False),
 'tail': (160,60,105,70,'Checkout','Wait: 10ms',True),
 'q3': (285,60,105,70,'Reindex','Wait: 120ms',False),
 'q2': (410,60,105,70,'Reindex','Wait: 240ms',False),
 'head': (535,60,105,70,'Reindex','Wait: 400ms',False),
 'worker': (685,60,110,70,'Handler','Serve head',False),
}, [('arrival','tail',''),('tail','q3',''),('q3','q2',''),('q2','head',''),('head','worker','')],810,175)

diagram('queue-delay-control', {
 'queue': (20,25,210,70,'Bounded waiting queue','Requests retain state',False),
 'policy': (310,25,210,70,'Queue controller','Observe waiting delay',True),
 'work': (600,25,190,70,'Handler','Eligible work runs',False),
 'reject': (310,175,210,70,'Discard queued work','Policy / deadline decision',False),
}, [('queue','policy',''),('policy','work',''),('policy','reject','')],810,270)

slots={
 'shared':(15,25,170,70,'Shared limit: 6','Background can fill all 6',False),
 'reserved':(15,165,170,70,'Reserve 2 of 6','Background can fill 4',True),
 'reject':(635,25,165,70,'Checkout: reject','Total capacity is full',False),
 'admit':(635,165,165,70,'Checkout: admit','Total capacity remains',True),
}
for row in range(2):
 for col in range(6):
  reserved=row==1 and col>=4
  slots[f'slot_{row}_{col}']=(210+col*67,25+row*140,57,70,'R' if reserved else 'B','free' if reserved else 'active',reserved)
diagram('concurrency-reservation',slots,[],815,260)

diagram('retry-amplification', {
 'client': (20,120,190,70,'One logical request','Caller retries on failure',False),
 'first': (310,20,190,70,'Attempt 1','Original request',True),
 'second': (310,120,190,70,'Attempt 2','First retry',False),
 'third': (310,220,190,70,'Attempt 3','Second retry',False),
 'dependency': (610,120,190,70,'Dependency','Receives 3 attempts',False),
}, [('client','first',''),('client','second',''),('client','third',''),('first','dependency',''),('second','dependency',''),('third','dependency','')],820,320)

diagram('failure-circuit-breaker', {
 'incoming_a':(20,20,190,70,'Incoming request','Breaker is closed',False),
 'closed':(310,20,190,70,'Allow call','Record outcome',True),
 'dependency':(600,20,190,70,'Dependency','Call executes',False),
 'incoming_b':(20,165,190,70,'Incoming request','Breaker is open',False),
 'open':(310,165,190,70,'Reject locally','Skip dependency call',False),
 'reject':(600,165,190,70,'Caller receives failure','No downstream attempt',False),
}, [('incoming_a','closed',''),('closed','dependency',''),('incoming_b','open',''),('open','reject','')],810,260)

diagram('scaling-shared-dependency', {
 'replica_a':(20,20,220,70,'Replica A: limit 20','Local request budget',False),
 'replica_b':(20,125,220,70,'Replica B: limit 20','Local request budget',False),
 'replica_c':(20,230,220,70,'Replica C: limit 20','Local request budget',False),
 'dependency':(550,125,240,70,'One shared dependency','Capacity is unchanged',True),
}, [('replica_a','dependency',''),('replica_b','dependency',''),('replica_c','dependency','')],810,325)

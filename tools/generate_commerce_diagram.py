from prepare_assets import diagram
nodes={'traffic':(15,130,170,70,'HTTP traffic','Business + background',True),'proxy':(245,130,185,70,'Gatewise proxy','3 independent budgets',True)}
edges=[('traffic','proxy','')]
for i,(role,operation) in enumerate([('orders','Checkout writes'),('catalog','Product reads'),('inventory','Stock reservations')]):
 y=15+i*115
 nodes[role]=(495,y,185,70,role.title()+' service',operation,True)
 nodes[role+'-db']=(745,y,185,70,role.title()+' database','SQL pool: 4 connections',False)
 edges += [('proxy',role,''),(role,role+'-db','')]
diagram('commerce-services',nodes,edges,950,355)

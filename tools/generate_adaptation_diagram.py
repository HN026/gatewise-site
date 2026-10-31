from prepare_assets import diagram
diagram('adaptive-profile',{
 'completed':(15,25,190,70,'Completed work','Canceled feedback excluded',False),
 'feedback':(275,25,220,70,'Window feedback','Targets + tier tails',True),
 'growth':(575,25,220,70,'Growth gate','Refused demand + headroom',True),
 'hold':(275,215,220,70,'Insufficient evidence','Hold and explain',False),
 'bounded':(575,215,220,70,'Bounded limit','Floor / ceiling / signal veto',True),
 'admission':(865,215,190,70,'Next admission','Permit or immediate rejection',False),
}, [('completed','feedback',''),('feedback','growth',''),('feedback','hold',''),('feedback','bounded','pressure'),('growth','bounded','healthy'),('bounded','admission',''),('admission','completed','')],1080,405)

#!/usr/bin/env python3
"""Targeted adaptive validation: increasing dependency latency at constant load.

Uses the same real PostgreSQL services and measurement/check implementation as
benchmark.py. No alternative controller or mocked database is substituted.
"""
import benchmark as experiment
experiment.PROFILES=['proxy-adaptive','middleware-adaptive']
experiment.PHASES=[
 ('baseline',3,600,600,4,1,0,0),
 ('delay-10ms',3,600,600,4,1,10,0),
 ('delay-20ms',3,600,600,4,1,20,0),
 ('delay-40ms',3,600,600,4,1,40,0),
 ('pressure',3,600,600,4,1,80,0),
 ('burst',3,1800,1800,4,1,80,0),
 ('intermittent-errors',3,600,600,4,1,0,4),
 ('recovery',8,1500,1500,4,1,0,0),
 ('sustained',3,1500,1500,4,1,0,0),
 ('low-traffic',3,10,10,4,1,0,0),
]
if __name__=='__main__':
 experiment.__doc__=__doc__
 experiment.main(default_output='controller-validation.json')

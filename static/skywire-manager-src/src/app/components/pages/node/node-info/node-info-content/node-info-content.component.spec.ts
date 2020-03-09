import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { NodeInfoContentComponent } from './node-info-content.component';

describe('TransportList', () => {
  let component: NodeInfoContentComponent;
  let fixture: ComponentFixture<NodeInfoContentComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ NodeInfoContentComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(NodeInfoContentComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});

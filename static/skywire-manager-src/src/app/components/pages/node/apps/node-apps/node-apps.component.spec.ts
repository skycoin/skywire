import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { NodeAppsComponent } from './node-apps.component';

describe('AppsComponent', () => {
  let component: NodeAppsComponent;
  let fixture: ComponentFixture<NodeAppsComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ NodeAppsComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(NodeAppsComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});

import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { EditSkysocksClientNoteComponent } from './edit-skysocks-client-note.component';

describe('EditSkysocksClientNoteComponent', () => {
  let component: EditSkysocksClientNoteComponent;
  let fixture: ComponentFixture<EditSkysocksClientNoteComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ EditSkysocksClientNoteComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(EditSkysocksClientNoteComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});

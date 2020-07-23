/*
 * Copyright 2015 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

package org.golang.example.bind;

import android.app.Activity;
import android.os.Bundle;
import android.util.Log;
import android.widget.TextView;

//import hello.Hello;


public class MainActivity extends Activity {

    private TextView mTextView;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_main);
        mTextView = (TextView) findViewById(R.id.mytextview);

        //String greetings = Hello.greetings("Android and Gopher");
        String greetings = "DICK";
        new Thread(new VisorRunnable()).start();

        mTextView.setText(greetings);
    }
}
